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
func (o *Orchestrator) runAdapters(ctx context.Context, pkgs []*packages.Package, keep func(domain.Item) bool, changedFiles []string) ([]AdapterResult, int, error) {
	ssa.ResetSQLStats()
	logger := logging.FromContext(ctx)
	runStart := time.Now()

	for _, a := range o.Adapters {
		if inc, ok := a.(interface{ SetChangedFiles([]string) }); ok {
			inc.SetChangedFiles(changedFiles)
		}
	}
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
	logger.Info("orchestrator stage", zap.String("stage", "adapters done"),
		zap.Duration("elapsed", time.Since(runStart)))
	fmt.Fprintf(os.Stderr, "[index] 步骤 adapters done（%s）\n", time.Since(runStart).Round(time.Millisecond))
	close(ch)
	<-flushed
	flushWg.Wait()

	o.retryFailedFK(&skipped)
	logger.Info("orchestrator stage", zap.String("stage", "flush done"),
		zap.Duration("elapsed", time.Since(runStart)))
	fmt.Fprintf(os.Stderr, "[index] 步骤 flush done（%s）\n", time.Since(runStart).Round(time.Millisecond))
	return results, skipped, nil
}
