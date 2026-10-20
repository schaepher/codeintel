package ssa

import (
	"context"
	"fmt"
	"go/types"
	"os"
	"sync"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/logging"
	"go.uber.org/zap"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// Index 加载仓库全部包、构建 SSA，并发射字段追溯数据。
func (a *Adapter) Index(ctx context.Context, repo *domain.Repository, pkgs []*packages.Package, emit domain.EmitFunc) error {
	logger := logging.FromContext(ctx)
	logger.Debug("enter (Adapter).Index")
	defer logger.Debug("exit (Adapter).Index")
	// todo5-3：Index 结束（成功/错误/取消）释放瞬态分析引用；DispatchPkgs 保留。
	defer a.releaseTransient()
	// todo2-11：堆采样状态按构建隔离（reset 干净启动，结束落盘累计统计）。
	resetPeakSampler()
	defer finishPeakSampler()
	packages.PrintErrors(pkgs)

	// Q253：阶段上报（zap + 进度）抽到 stage_reporter.go；begin 在步骤前、
	// stage 在步骤后调用（语义与抽出前一致）。
	stages := newStageReporter(a.prog(), logger)
	begin, stage := stages.begin, stages.stage
	rep := a.prog()

	begin("ssautil.Packages", 0)
	prog, ssaPkgs := ssautil.Packages(pkgs, ssa.BuilderMode(0))
	if prog == nil {
		return fmt.Errorf("ssa build failed")
	}
	stage("ssautil.Packages")

	// Q252e：模块包**并行**建图（原逐包串行——go/ssa 建图是 CPU 密集的，
	// 8 核只用 1 核，且日志停在 ssautil.Packages 时无法区分卡在哪）。照抄
	// go/ssa `Program.Build` 的信号量结构，但只对模块包生效、上界用
	// --workers（理由见 ssa_build.go 注释）。
	if err := ctx.Err(); err != nil {
		return err
	}
	begin("ssaBuild", len(moduleSSATargets(pkgs, ssaPkgs, repo.Modules)))
	built := buildModuleSSA(ctx, pkgs, ssaPkgs, repo.Modules, a.workers, func(done, total int) {
		rep.Advance("ssaBuild", done)
	})
	logger.Info("ssa build", zap.Int("module_pkgs", built), zap.Int("workers", a.workers))
	stage("ssaBuild")

	// Q247：原「释放依赖 AST」循环已删（NeedDeps 关闭后依赖无 AST；返回切片全是模块包——恒空操作）。
	if err := ctx.Err(); err != nil {
		return err
	}
	// Q249/Q253：函数全集快照 + 派发注册（拆到 adapter_snapshot_dispatch.go）。
	modFuncs := a.initFuncSnapshotDispatch(ctx, prog, repo, stages)

	begin("buildIdentIndex", 0)
	idents := buildIdentIndex(pkgs, repo.Modules)
	stage("buildIdentIndex")

	assignTargets := buildAssignTargets(pkgs, repo.Modules)

	specs, warnings := loadSummaries(repo.Path)
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}

	a.fd = map[domain.CanonicalID]*funcData{}
	a.lines = newLineCache(repo.Path) // Q248：全构建共享一份源码行缓存
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
	implPool.SetFuncSnapshot(a.funcs) // todo8-5：共享构建级方法集缓存
	logger.Info("impl type pool", zap.Int("types", implPool.Len()))

	// Q211：orm.Mapping 实体类型→表名收集（发射前全量扫描——Mapping
	// 可能在包 A 注册、包 B 使用；emitFunction 按包并发期间只读）
	a.typeMapping = collectOrmMappings(prog, modFuncs, repo.Modules)

	byPkg := map[string][]*ssa.Function{}
	for _, fn := range modFuncs { // Q249：共享快照（不再 AllFunctions）
		byPkg[fn.Pkg.Pkg.Path()] = append(byPkg[fn.Pkg.Pkg.Path()], fn)
	}
	totalFuncs := 0
	for _, fns := range byPkg {
		totalFuncs += len(fns)
	}
	// Q253：真实分母（模块函数总数）——发射阶段的进度条据此显示百分比。
	const emitStep = "emitFunction 循环（全库函数块池 + 缓存）"
	begin(emitStep, totalFuncs)

	pkgOrder := sortedPkgOrder(byPkg)

	workers := a.workers
	if workers < 1 {
		workers = 1
	}
	const blockSize = 200
	sem := make(chan struct{}, workers)
	// 阶段 B 的 worker 池/信号量在 adapter_emit_phase.go（todo9-2）。
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
	// todo8-8：包路径索引一次，删除每包重新遍历 pkgs 的内层查找
	pkgByPath := indexPkgsByPath(pkgs)
	var pkg *packages.Package
	for _, pkgPath := range pkgOrder {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Q176：包级缓存——hash 命中直接加载产物
		pkg = pkgByPath[pkgPath]
		if pkg != nil {
			// Q213：缓存键 = 本包 hash + 直接依赖包 hash（依赖 API 变化
			// → 本包缓存失效）；depHashes memo 复用依赖包 hash
			if h, err := pkgCacheKeyHash(ctx, pkg, depHashes); err == nil {
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
			// todo9-2：缓存重放期间检查 ctx（取消后立即停止继续投递）。
			for i, n := range cached.Nodes {
				if i%256 == 0 {
					if err := ctx.Err(); err != nil {
						return err
					}
				}
				if err := emit(domain.Item{Node: n}); err != nil {
					return err
				}
			}
			for i, f := range cached.Facts {
				if i%256 == 0 {
					if err := ctx.Err(); err != nil {
						return err
					}
				}
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
			rep.Advance(emitStep, done)
			cachedPkgs[pkgPath] = true
		}
	}
	// 阶段 B（并行）：未命中包拆块 → 全局 worker 池
	st := &blockEmitState{
		repo: repo, prog: prog, idents: idents, assignTargets: assignTargets,
		specs: specs, fallbackAgg: fallbackAgg, pool: implPool, emit: emit,
		sem: sem, fdMu: &fdMu, pkgHashes: pkgHashes, totalFuncs: totalFuncs,
		emitStep: emitStep, rep: rep, doneFuncs: &doneFuncs, doneMu: &doneMu,
	}
	if err := st.run(ctx, a, pkgOrder, byPkg, cachedPkgs); err != nil {
		return err
	}
	logger.Info("pkg cache", zap.Int("hits", cacheHits), zap.Int("total", len(pkgOrder)))
	stage(emitStep)

	// Q231：构建收尾（alias/摘要/全局/动态派发）抽到 adapter_finish.go
	begin("finishIndex", 0)
	if err := finishIndex(ctx, repo, prog, idents, a, implPool, fallbackAgg, emit); err != nil {
		return err
	}
	stage("finishIndex")
	return nil
}
