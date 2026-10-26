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

// setSCIPPackagePatterns 向支持增量包范围的适配器注入变更包 patterns（P1-12）。
// pkgPatterns 键为 module 相对目录（根为 "."），值为该 module 下的
// package-pattern（如 "./internal/impl/passport"）。
func (o *Orchestrator) setSCIPPackagePatterns(pats pkgPatterns) {
	var m map[string][]string
	if len(pats) > 0 {
		m = map[string][]string(pats)
	}
	for _, a := range o.Adapters {
		if s, ok := a.(interface{ SetPackagePatterns(map[string][]string) }); ok {
			s.SetPackagePatterns(m)
		}
	}
}

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
	// P1-08：全量构建先算一次分析范围指纹（内容 hash 口径与 SSA 包缓存
	// 一致），注入 SSA（复用，避免重复读盘 hash）与 SCIP（产物缓存键）；
	// 增量构建（包集合是子集，范围不同）不注入内容指纹，改用 P1-12 的
	// 变更包 patterns。指纹计算失败 → 不影响构建（两适配器各自回退）。
	if len(changedFiles) == 0 {
		if scope, err := ssa.BuildAnalysisScope(ctx, pkgs, o.Repo.Modules); err != nil {
			logger.Warn("analysis scope fingerprint failed; package/scip caches disabled for this build", zap.Error(err))
		} else {
			for _, a := range o.Adapters {
				if s, ok := a.(interface{ SetAnalysisScope(*ssa.AnalysisScope) }); ok {
					s.SetAnalysisScope(scope)
				}
				if c, ok := a.(interface{ SetContentFingerprint(string) }); ok {
					c.SetContentFingerprint(scope.Fingerprint())
				}
			}
		}
	}
	rep := o.prog()
	if fkOff {
		if err := o.RepoImpl.SetForeignKeys(false); err != nil {
			logger.Warn("disable foreign keys for build", zap.Error(err))
		}
	}
	// Q?：开启构建期端点正缓存（build_write_cache.go）——flush 单写者，
	// 事务提交成功才发布正向键；构建结束无论成败都清空（不跨构建复用）。
	o.RepoImpl.BeginBuildWriteCache()
	defer func() {
		st := o.RepoImpl.EndBuildWriteCache()
		logger.Info("build write cache",
			zap.Int64("inputs", st.Inputs), zap.Int64("writes", st.Writes),
			zap.Int64("hits", st.Hits), zap.Int64("rotations", st.Rotations), zap.Int64("size", st.Size))
	}()
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

	// flush 协程（单写者：SQLite 写锁串行）。P1-13：记录每批耗时，
	// 构建结束给 p50/p95/max（区分"少数长尾"与"每批都慢"）。
	var flushWg sync.WaitGroup
	var flushLats []time.Duration
	flushWg.Add(1)
	go func() {
		defer flushWg.Done()
		for b := range flushCh {
			started := time.Now()
			if err := o.flush(b, &mu, &skipped); err != nil {
				fmt.Fprintf(os.Stderr, "write batch: %v\n", err)
			}
			flushLats = append(flushLats, time.Since(started))
			putBatch(b)
		}
	}()

	consumeStart := time.Now()
	consumeCount := 0
	batches := 0
	go func() {
		defer close(flushed)
		batch := getBatch()
		for item := range ch {
			if keep != nil && !keep(item) {
				continue
			}
			if item.Node != nil {
				if batch.dedup.shouldAppend(item.Node) {
					batch.nodes = append(batch.nodes, item.Node)
				}
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
				batch = getBatch()
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
	adapterTimeout := o.adapterTimeoutOrDefault()
	var wg sync.WaitGroup
	for _, a := range o.Adapters {
		wg.Add(1)
		go func(adapter domain.IndexerPort) {
			defer wg.Done()
			adapterCtx, cancel := context.WithTimeout(ctx, adapterTimeout)
			defer cancel()
			r := AdapterResult{Name: adapter.Name()}
			adapterStart := time.Now()
			rep.Begin(r.Name, 1, 0)
			r.Err = adapter.Index(adapterCtx, o.Repo, pkgs, func(item domain.Item) error {
				// todo9-2：适配器超时后必须停止接收发射——否则超时的
				// 适配器仍会继续投递（外层 ctx 未取消时永远阻塞/写入）。
				// 先显式查 Err：select 在 channel 可写时会随机选中发送，
				// 不能保证 Done 优先。
				if err := adapterCtx.Err(); err != nil {
					return err
				}
				select {
				case ch <- item:
					return nil
				case <-adapterCtx.Done():
					return adapterCtx.Err()
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
	// P1-13：写库批次延迟分布（单写者串行路径，最近秩分位数）。
	if p50, p95, maxLat := flushLatency(flushLats); len(flushLats) > 0 {
		logger.Info("flush latency",
			zap.Int("batches", len(flushLats)),
			zap.Duration("p50", p50), zap.Duration("p95", p95), zap.Duration("max", maxLat))
	}
	rep.End("flush done", time.Since(flushStart), nil)
	logger.Info("orchestrator stage", zap.String("stage", "flush done"),
		zap.Duration("elapsed", time.Since(runStart)))
	return results, skipped, nil
}
