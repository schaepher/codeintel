package orchestrator

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/ssa"
	"github.com/schaepher/codeintel/internal/logging"
	"go.uber.org/zap"
	"golang.org/x/tools/go/packages"
)

// runAdapters 并行执行适配器并写库（keep 为 nil 时全部写入；否则只保留
// keep(item) 为 true 的条目）。pkgs 为共享加载的 go/packages 结果
// （AST/SSA 复用，避免重复类型检查）。返回各适配器结果与跳过的 FK 冲突边数。
// fkOff（Q254 Stage 1）：构建期关闭外键校验——每行 2 次父表探测的开销去掉，
// 且悬挂边不再走 retryFailedFK（实测 20.7 万条边全程驻留内存）。末尾用
// DropDanglingEdges 一次性清理并计入 skipped，语义与之前一致（悬挂边不进图）。
func (o *Orchestrator) runAdapters(ctx context.Context, pkgs []*packages.Package, keep func(domain.Item) bool, changedFiles []string, fkOff bool) ([]AdapterResult, int, error) {
	ssa.ResetSQLStats()
	logger := logging.FromContext(ctx)
	runStart := time.Now()

	for _, a := range o.Adapters {
		if inc, ok := a.(interface{ SetChangedFiles([]string) }); ok {
			inc.SetChangedFiles(changedFiles)
		}
	}
	rep := o.prog()
	if fkOff {
		if err := o.RepoImpl.SetForeignKeys(false); err != nil {
			logger.Warn("disable foreign keys for build", zap.Error(err))
		}
	}
	// "adapters done" 聚合步骤：elapsed 覆盖整个并行适配器阶段（与 Q253 前
	// 同一个数字），只是改由进度渲染器输出。
	rep.Begin("adapters done", 0, 0)
	var (
		results []AdapterResult
		skipped int
		mu      sync.Mutex
	)

	ch := make(chan domain.Item, 4096)
	// Q174：backpressure 采集——producer 因 channel 满阻塞的时间占比
	// （flush 慢时生产 worker 阻塞于写库；日志显示 CPU vs wall）
	var bpTotal time.Duration
	var bpMu sync.Mutex
	flushCh := make(chan *batchT, 2)
	flushed := make(chan struct{})

	// flush 协程（单写者：SQLite 写锁串行）
	var flushWg sync.WaitGroup
	flushWg.Add(1)
	go func() {
		defer flushWg.Done()
		for b := range flushCh {
			if err := o.flush(b, &mu, &skipped); err != nil {
				fmt.Fprintf(os.Stderr, "write batch: %v\n", err)
			}
		}
	}()

	consumeStart := time.Now()
	consumeCount := 0
	batches := 0
	go func() {
		defer close(flushed)
		batch := newBatch()
		for item := range ch {
			if keep != nil && !keep(item) {
				continue
			}
			if item.Node != nil {
				batch.nodes = append(batch.nodes, item.Node)
			}
			if item.Fact != nil {
				batch.edges = append(batch.edges, item.Fact)
			}
			if item.Summary != nil {
				batch.summaries = append(batch.summaries, item.Summary)
			}
			if item.Origins != nil {
				batch.origins = append(batch.origins, item.Origins...)
			}
			consumeCount++
			if consumeCount%500 == 0 {
				logger.Info("consume progress", zap.Int("items", consumeCount),
					zap.Duration("elapsed", time.Since(consumeStart)))
			}
			if len(batch.nodes) >= BatchSize || len(batch.edges) >= BatchSize || len(batch.summaries) >= BatchSize || len(batch.origins) >= BatchSize {
				flushCh <- batch
				batch = newBatch()
				batches++
				// Q254：每 50 批做一次非阻塞 checkpoint（WAL 小时是廉价空操作，
				// 防构建期 WAL 无限增长；有读者持快照时返回 busy 不报错）
				if batches%50 == 0 {
					if _, err := o.RepoImpl.CheckpointPassive(); err != nil {
						logger.Debug("periodic wal checkpoint", zap.Error(err))
					}
				}
			}
		}
		flushCh <- batch
		close(flushCh)
	}()

	// 并行跑适配器（独立超时，失败不中断他人）
	var wg sync.WaitGroup
	for _, a := range o.Adapters {
		wg.Add(1)
		go func(adapter domain.IndexerPort) {
			defer wg.Done()
			adapterCtx, cancel := context.WithTimeout(ctx, AdapterTimeout)
			defer cancel()
			r := AdapterResult{Name: adapter.Name()}
			adapterStart := time.Now()
			rep.Begin(r.Name, 1, 0)
			r.Err = adapter.Index(adapterCtx, o.Repo, pkgs, func(item domain.Item) error {
				select {
				case ch <- item:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})

			if p, ok := adapter.(interface{ DispatchPkgs() []string }); ok {
				r.DispatchPkgs = p.DispatchPkgs()
			}
			rep.End(r.Name, time.Since(adapterStart), r.Err)

			bpMu.Lock()
			bpTotal += time.Since(adapterStart)
			bpMu.Unlock()
			r.Duration = time.Since(adapterStart)
			mu.Lock()
			results = append(results, r)
			mu.Unlock()
		}(a)
	}
	wg.Wait()
	rep.End("adapters done", time.Since(runStart), nil)
	logger.Info("orchestrator stage", zap.String("stage", "adapters done"),
		zap.Duration("elapsed", time.Since(runStart)))
	flushStart := time.Now()
	rep.Begin("flush done", 0, 0)
	close(ch)
	<-flushed
	flushWg.Wait()

	o.retryFailedFK(&skipped)
	if fkOff {
		if n, err := o.RepoImpl.DropDanglingEdges(5000); err != nil {
			logger.Warn("drop dangling edges", zap.Error(err))
		} else if n > 0 {
			skipped += n
			logger.Info("dropped dangling edges", zap.Int("edges", n))
		}
		if err := o.RepoImpl.SetForeignKeys(true); err != nil {
			logger.Warn("restore foreign keys", zap.Error(err))
		}
	}
	// Q254：WAL 收尾——真实库构建末尾 WAL 曾涨到 1.33GB（未设上限 +
	// checkpoint 跟不上）；此处显式截断（有读者时 busy，仅告警不失败）。
	if _, err := o.RepoImpl.CheckpointTruncate(); err != nil {
		logger.Warn("wal checkpoint truncate", zap.Error(err))
	}
	rep.End("flush done", time.Since(flushStart), nil)
	logger.Info("orchestrator stage", zap.String("stage", "flush done"),
		zap.Duration("elapsed", time.Since(runStart)))
	return results, skipped, nil
}
