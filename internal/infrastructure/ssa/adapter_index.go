package ssa

import (
	"context"
	"fmt"
	"go/types"
	"os"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/logging"
	"go.uber.org/zap"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// SetWorkers 设置按包并发数（Q170：--workers 参数；≤1 退串行）。
func (a *Adapter) SetWorkers(n int) {
	a.workers = n
}

// Name 实现 IndexerPort。
func (a *Adapter) Name() string {
	return "ssa"
}

// Index 加载仓库全部包、构建 SSA，并发射字段追溯数据。
func (a *Adapter) Index(ctx context.Context, repo *domain.Repository, pkgs []*packages.Package, emit domain.EmitFunc) error {
	logger := logging.FromContext(ctx)
	logger.Debug("enter (Adapter).Index")
	defer logger.Debug("exit (Adapter).Index")
	packages.PrintErrors(pkgs)

	stageStart := time.Now()
	stage := func(name string) {
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		logger.Info("build stage",
			zap.String("stage", name), zap.Duration("elapsed", time.Since(stageStart)),
			zap.Int64("heap_mb", int64(ms.HeapAlloc>>20)),
			zap.Int64("heap_inuse_mb", int64(ms.HeapInuse>>20)))
		// 命令执行界面展示（zap 未初始化是 noop——直接 stderr 实时可见）。
		// Q247：HeapAlloc 含**未回收垃圾**（阶段边界未 GC）只当趋势看，
		// 真实 live 构成用 CODEINTEL_MEM_PROFILE 的 heap profile 归因。
		fmt.Fprintf(os.Stderr, "[index] ssa 步骤 %s（%s, heap %dMB/%dMB inuse）\n",
			name, time.Since(stageStart).Round(time.Millisecond),
			int64(ms.HeapAlloc>>20), int64(ms.HeapInuse>>20))
		dumpPeakHeapProfile(ms.HeapAlloc)
		stageStart = time.Now()
	}

	prog, ssaPkgs := ssautil.Packages(pkgs, ssa.BuilderMode(0))
	if prog == nil {
		return fmt.Errorf("ssa build failed")
	}
	stage("ssautil.Packages")

	for i, p := range pkgs {
		if !isInModule(p.PkgPath, repo.Modules) {
			continue
		}
		if sp := ssaPkgs[i]; sp != nil {
			sp.Build()
		}
	}

	// Q247：原「释放依赖 AST」循环已删（NeedDeps 关闭后依赖无 AST；返回切片全是模块包——恒空操作）。
	// Q221：dispatchRegs 必须在 Index 级初始化一次（sp.Build 之后——
	// MakeInterface 指令已构建）——零值 nil map 经 extractor 解引用
	// 复制后 `ext.dispatchRegs == nil` 永远成立，cf_call.go 懒初始化
	// 兜底导致每个函数都全程序 AllFunctions 扫描（go2o 12875 函数 ×
	// 全图遍历 ≈ 305s CPU，pprof 46% 热点）。初始化后各 extractor
	// 共享只读 map。
	a.dispatchPkgs = nil
	a.dispatchRegs, a.dispatchPkgs = collectDispatchRegistrations(prog, repo.Modules)
	a.regHits = buildRegHits(a.dispatchRegs, prog)

	idents := buildIdentIndex(pkgs, repo.Modules)
	stage("buildIdentIndex")

	assignTargets := buildAssignTargets(pkgs, repo.Modules)

	specs, warnings := loadSummaries(repo.Path)
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}

	a.fd = map[domain.CanonicalID]*funcData{}
	fallbackAgg := newFallbackAgg()
	// 接口动态派发候选枚举用（⑮：模块内类型池）——Q246：池与结果 memo
	// Index 级构建一次（旧的 implMethodsFor 每个调用点重扫全部包 scope）
	var typePkgs []*types.Package
	for _, p := range pkgs {
		if p.Types != nil {
			typePkgs = append(typePkgs, p.Types)
		}
	}
	implPool := newImplTypePool(typePkgs, repo.Modules)
	logger.Info("impl type pool", zap.Int("types", implPool.Len()))

	// Q211：orm.Mapping 实体类型→表名收集（发射前全量扫描——Mapping
	// 可能在包 A 注册、包 B 使用；emitFunction 按包并发期间只读）
	a.typeMapping = collectOrmMappings(prog, repo.Modules)

	byPkg := map[string][]*ssa.Function{}
	for fn := range ssautil.AllFunctions(prog) {
		if !isModuleFunction(fn, repo.Modules) {
			continue
		}
		byPkg[fn.Pkg.Pkg.Path()] = append(byPkg[fn.Pkg.Pkg.Path()], fn)
	}
	totalFuncs := 0
	for _, fns := range byPkg {
		totalFuncs += len(fns)
	}

	pkgOrder := make([]string, 0, len(byPkg))
	for pkgPath := range byPkg {
		pkgOrder = append(pkgOrder, pkgPath)
	}
	sort.Slice(pkgOrder, func(i, j int) bool {
		return len(byPkg[pkgOrder[i]]) > len(byPkg[pkgOrder[j]])
	})

	workers := a.workers
	if workers < 1 {
		workers = 1
	}
	const blockSize = 200
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	var fdMu sync.Mutex // 合并保护（mergeFuncData）
	doneFuncs := 0
	var doneMu sync.Mutex
	cacheHits := 0
	depHashes := map[string]string{} // Q213：直接依赖包 hash memo（每包一次）
	// Q221：包间并行——原实现包循环串行 + 包内分块并行：小包（<200
	// 函数）只有 1 块，同一时刻仅 1 个 goroutine 干活（go2o 135 包
	// 平均 95 函数/包 → 8 核只用了 1 核，3m22s 仅加速 1.56 倍）。
	// 改为全库函数块池：未命中包拆块后全部进 worker 池并行（块内单包
	// ——产物按包收集写缓存），8 核满负荷。
	//
	// 阶段 A（串行）：包 hash + 缓存命中直接加载产物
	pkgHashes := map[string]string{} // Q221：块处理完后保存缓存用
	cachedPkgs := map[string]bool{}
	var pkg *packages.Package
	for _, pkgPath := range pkgOrder {
		// Q176：包级缓存——hash 命中直接加载产物
		pkg = nil
		for i := range pkgs {
			if pkgs[i].PkgPath == pkgPath {
				pkg = pkgs[i]
				break
			}
		}
		if pkg != nil {
			// Q213：缓存键 = 本包 hash + 直接依赖包 hash（依赖 API 变化
			// → 本包缓存失效）；depHashes memo 复用依赖包 hash
			if h, err := pkgCacheKeyHash(pkg, depHashes); err == nil {
				pkgHashes[pkgPath] = h
			} else {
				logger.Warn("pkg cache key hash failed", zap.String("pkg", pkgPath), zap.Error(err))
			}
		}
		hash := pkgHashes[pkgPath]
		if hash == "" {
			continue
		}
		fns := byPkg[pkgPath]
		if cached := loadPkgCache(pkgCachePath(repo.Path, pkgPath), hash); cached != nil {
			for _, n := range cached.Nodes {
				if err := emit(domain.Item{Node: n}); err != nil {
					return err
				}
			}
			for _, f := range cached.Facts {
				if err := emit(domain.Item{Fact: f}); err != nil {
					return err
				}
			}
			for id, cfd := range cached.FuncData {
				mergeFuncData(&fdMu, a.fd, domain.CanonicalID(id), fromCachedFD(cfd))
			}
			doneMu.Lock()
			doneFuncs += len(fns)
			cacheHits++
			done := doneFuncs
			percent := done * 100 / totalFuncs
			doneMu.Unlock()
			logger.Info("build progress",
				zap.String("pkg", pkgPath), zap.Int("funcs", len(fns)),
				zap.Int("done", done), zap.Int("total", totalFuncs),
				zap.Int("percent", percent), zap.Bool("cached", true))
			cachedPkgs[pkgPath] = true
		}
	}
	// 阶段 B（并行）：未命中包拆块 → 全局 worker 池
	type fnBlock struct {
		pkgPath string
		idx     int // 包内块序号（块产物按序拼接写入缓存——并发完成顺序不定）
		fns     []*ssa.Function
	}
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
	// wg.Wait() 后统一写——go2o 实测占了峰值堆的近 1GB，3GB 机器上直接
	// 换页/OOM）。落盘在锁外（写文件不阻塞其他块）。
	var collectMu sync.Mutex
	colSet := newPkgCollectSet(repo.Path, pkgBlockCount, pkgHashes)
	blockStart := time.Now()
	for _, blk := range blocks {
		wg.Add(1)
		go func(blk fnBlock) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			var blkNodes []*domain.CodeEntity
			var blkFacts []*domain.Fact
			blkFD := map[domain.CanonicalID]*funcData{}
			pkgEmit := func(item domain.Item) error {
				// 块内单 goroutine 顺序收集（无锁）
				if item.Node != nil {
					blkNodes = append(blkNodes, item.Node)
				}
				if item.Fact != nil {
					blkFacts = append(blkFacts, item.Fact)
				}
				return emit(item)
			}
			for _, fn := range blk.fns {
				owner, fd, err := emitFunction(repo, prog, fn, idents, assignTargets, specs, fallbackAgg, pkgEmit, implPool, &a.dispatchRegs, a.regHits, a.typeMapping)
				if err != nil {
					fmt.Fprintf(os.Stderr, "emitFunction %s: %v\n", fn.Name(), err)
					return
				}
				if owner != "" && fd != nil {
					blkFD[owner] = fd
					mergeFuncData(&fdMu, a.fd, owner, fd)
				}
			}
			// 块产物按块序号存入包收集器；该包最后一块到达 → 整包写缓存
			collectMu.Lock()
			pc := colSet.get(blk.pkgPath)
			pc.blocks[blk.idx] = blkNodes
			pc.facts[blk.idx] = blkFacts
			pc.fd[blk.idx] = blkFD
			pc.done++
			ready := pc.done == pc.total
			if ready {
				colSet.release(blk.pkgPath) // 释放：后续块不会再引用
			}
			collectMu.Unlock()
			if ready {
				pc.save()
			}
			doneMu.Lock()
			doneFuncs += len(blk.fns)
			done := doneFuncs
			percent := done * 100 / totalFuncs
			doneMu.Unlock()
			logger.Info("build progress",
				zap.String("pkg", blk.pkgPath), zap.Int("funcs", len(blk.fns)),
				zap.Int("done", done), zap.Int("total", totalFuncs),
				zap.Int("percent", percent),
				zap.Duration("elapsed", time.Since(blockStart)))
		}(blk)
	}
	wg.Wait()
	logger.Info("pkg cache", zap.Int("hits", cacheHits), zap.Int("total", len(pkgOrder)))
	stage("emitFunction 循环（全库函数块池 + 缓存）")

	// Q231：构建收尾（alias/摘要/全局/动态派发）抽到 adapter_finish.go
	if err := finishIndex(repo, prog, idents, a, implPool, fallbackAgg, emit); err != nil {
		return err
	}
	stage("finishIndex")
	return nil
}
