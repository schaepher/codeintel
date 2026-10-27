package ssa

// todo9-2：SSA 发射阶段 B（未命中包拆块 → 全局 worker 池，从
// adapter_index.go 拆出——行数治理）。取消语义：
//   - 等待信号量/发射等待都 select ctx（停止排队/投递）
//   - 取消/worker 错误的包标记 aborted，不落盘半包缓存
//   - 错误向上传播（Index 返回，不再只打 stderr）

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
	"golang.org/x/tools/go/ssa"
)

// fnBlock 包内函数块（块产物按块序号拼接写缓存——并发完成顺序不定）。
type fnBlock struct {
	pkgPath string
	idx     int
	fns     []*ssa.Function
}

// blockEmitState 发射阶段 B 的共享输入（Index 组装一次）。
type blockEmitState struct {
	repo          *domain.Repository
	prog          *ssa.Program
	idents        *identIndex
	assignTargets []assignTarget
	specs         map[string]summarySpec
	fallbackAgg   *fallbackAgg
	pool          *implTypePool
	emit          domain.EmitFunc
	sem           chan struct{}
	fdMu          *sync.Mutex
	pkgHashes     map[string]string
	totalFuncs    int
	emitStep      string
	rep           domain.Progress
	doneFuncs     *int
	doneMu        *sync.Mutex
}

// run 拆块并并发发射，返回首个错误（ctx 取消 / emitFunction 失败）。
func (s *blockEmitState) run(ctx context.Context, a *Adapter, pkgOrder []string,
	byPkg map[string][]*ssa.Function, cachedPkgs map[string]bool) error {
	const blockSize = 200
	var blocks []fnBlock
	pkgBlockCount := map[string]int{}
	for _, pkgPath := range pkgOrder {
		if cachedPkgs[pkgPath] {
			continue
		}
		fns := byPkg[pkgPath]
		n := 0
		for start := 0; start < len(fns); start += blockSize {
			end := start + blockSize
			if end > len(fns) {
				end = len(fns)
			}
			blocks = append(blocks, fnBlock{pkgPath: pkgPath, idx: n, fns: fns[start:end]})
			n++
		}
		pkgBlockCount[pkgPath] = n
	}
	// 包级产物收集（写缓存用）：map[pkgPath] → 该包全部块产物。Q246：
	// 每个包**最后一块**完成即落盘写缓存并释放（原实现把全量产物留到
	// wg.Wait() 后统一写——go2o 实测占了峰值堆的近 1GB）。
	var collectMu sync.Mutex
	colSet := newPkgCollectSet(s.repo.Path, pkgBlockCount, s.pkgHashes)
	var emitErr error
	var emitErrMu sync.Mutex
	var wg sync.WaitGroup
	blockStart := time.Now()
	abort := func(pkgPath string, err error) {
		emitErrMu.Lock()
		if emitErr == nil {
			emitErr = err
		}
		emitErrMu.Unlock()
		collectMu.Lock()
		colSet.abort(pkgPath)
		collectMu.Unlock()
	}
	for _, blk := range blocks {
		wg.Add(1)
		go func(blk fnBlock) {
			defer wg.Done()
			// 等待 worker 令牌时支持取消（不继续排队新任务；取得后发现
			// 已取消则立即归还，见 acquireWorker）。
			if !acquireWorker(ctx, s.sem) {
				abort(blk.pkgPath, ctx.Err())
				return
			}
			defer func() { <-s.sem }()
			var blkNodes []*domain.CodeEntity
			var blkFacts []*domain.Fact
			blkFD := map[domain.CanonicalID]*funcData{}
			var blkFDMu sync.Mutex // Q252：块内合并（闭包归外层 → 同 owner 多次）
			pkgEmit := func(item domain.Item) error {
				// 发射等待同样响应适配器取消（避免取消后仍继续产出/阻塞）。
				if err := ctx.Err(); err != nil {
					return err
				}
				if item.Node != nil {
					blkNodes = append(blkNodes, item.Node)
				}
				if item.Fact != nil {
					blkFacts = append(blkFacts, item.Fact)
				}
				return s.emit(item)
			}
			for _, fn := range blk.fns {
				if err := ctx.Err(); err != nil {
					abort(blk.pkgPath, err)
					return
				}
				owner, fd, err := emitFunction(s.repo, s.prog, fn, s.idents, s.assignTargets, s.specs,
					s.fallbackAgg, pkgEmit, s.pool, a.lines, a.funcs, &a.dispatchRegs, a.regHits, a.typeMapping)
				if err != nil {
					fmt.Fprintf(os.Stderr, "emitFunction %s: %v\n", fn.Name(), err)
					abort(blk.pkgPath, err)
					return
				}
				if owner != "" && fd != nil {
					// Q252：缓存收集与全局 a.fd 同一合并语义（追加）。
					mergeFuncData(&blkFDMu, blkFD, owner, fd)
					mergeFuncData(s.fdMu, a.fd, owner, fd)
				}
			}
			collectMu.Lock()
			pc := colSet.get(blk.pkgPath)
			pc.blocks[blk.idx] = blkNodes
			pc.facts[blk.idx] = blkFacts
			pc.fd[blk.idx] = blkFD
			pc.done++
			ready := pc.done == pc.total
			if ready {
				colSet.release(blk.pkgPath)
			}
			collectMu.Unlock()
			if ready {
				pc.save()
			}
			s.doneMu.Lock()
			*s.doneFuncs += len(blk.fns)
			done := *s.doneFuncs
			percent := done * 100 / s.totalFuncs
			s.doneMu.Unlock()
			zap.L().Info("build progress",
				zap.String("pkg", blk.pkgPath), zap.Int("funcs", len(blk.fns)),
				zap.Int("done", done), zap.Int("total", s.totalFuncs),
				zap.Int("percent", percent),
				zap.Duration("elapsed", time.Since(blockStart)))
			s.rep.Advance(s.emitStep, done)
		}(blk)
	}
	wg.Wait()
	if emitErr != nil {
		return emitErr
	}
	return ctx.Err()
}
