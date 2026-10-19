package ssa

import (
	"go/types"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
	"golang.org/x/tools/go/ssa"
)

// emitDispatches 发射全部 dispatch_to 边：
//  1. 收集模块内 MakeInterface 注册点
//  2. 遍历模块内函数的所有动态接口方法调用（cc.Method != nil）
//  3. 候选 = 注册点命中（register 0.9）∪ 枚举实现者（enum 0.7）
//  4. 接口类型节点 → 候选实现方法（模块内）→ dispatch_to 边
//
// P0-2：返回 dispatch 相关模块内包路径（注册点包 ∪ 动态调用包）——
// 增量构建补 Load 用（这些包未 Load 时 dispatch_to 边整体丢失）。
// todo7-1：regs/regPkgs 由 initFuncSnapshotDispatch 已收集的注册结果传入，
// 避免收尾阶段重复全程序扫描。nil = 未初始化（直接调用/测试）→ 回退扫描
// 一次；**非 nil 空 map** = 已初始化但无注册点 → 不得再次扫描。
func emitDispatches(repo *domain.Repository, prog *ssa.Program, pool *implTypePool, funcs *funcSnapshot,
	regs dispatchReg, regPkgs []string, emit domain.EmitFunc) ([]string, error) {
	logger := zap.L()
	logger.Debug("enter emitDispatches")
	defer logger.Debug("exit emitDispatches")
	if regs == nil {
		regs, regPkgs = collectDispatchRegistrations(prog, funcs.moduleFuncs(repo.Modules), repo.Modules, funcs, nil)
	}

	// 接口方法调用集合：接口类型 → 方法名（map 去重；UNIQUE 边合并）
	type callKey struct {
		iface  *types.Named
		method string
	}
	calls := map[callKey]bool{}
	callPkgSet := map[string]bool{}
	for _, p := range regPkgs {
		callPkgSet[p] = true
	}
	for _, fn := range funcs.moduleFuncs(repo.Modules) { // Q249：共享快照
		for _, b := range fn.Blocks {
			for _, instr := range b.Instrs {
				var cc *ssa.CallCommon
				switch v := instr.(type) {
				case *ssa.Call:
					cc = &v.Call
				case *ssa.Go:
					cc = &v.Call
				case *ssa.Defer:
					cc = &v.Call
				}
				if cc == nil || cc.Method == nil || cc.StaticCallee() != nil {
					continue
				}
				if named := interfaceNamedOf(cc.Value.Type()); named != nil {
					calls[callKey{iface: named, method: cc.Method.Name()}] = true
					if fn.Pkg != nil && isInModule(fn.Pkg.Pkg.Path(), repo.Modules) {
						callPkgSet[fn.Pkg.Pkg.Path()] = true
					}
				}
			}
		}
	}
	if len(calls) == 0 {
		// 无动态接口调用时仍返回注册点包集——否则 meta.DispatchPkgs 变空，
		// 后续增量不再补 Load 注册点包，dispatch_to 边永久丢（P0-2 根治）。
		return regPkgs, nil
	}

	dispatchPkgs := make([]string, 0, len(callPkgSet))
	for p := range callPkgSet {
		dispatchPkgs = append(dispatchPkgs, p)
	}

	// todo8-6：按 (接口, 动态类型) 预计算最佳注册位置（relPath 归一化 +
	// (file,line) 字典序最小）。同一动态类型的不同方法复用该结果，不再
	// 逐方法重扫位置并调用 relPath；完整 dispatchReg 位置列表保留不动。
	type normSite struct {
		file string
		line int
	}
	bestSites := make(map[*types.Named]map[string]normSite, len(regs))
	for iface, byDyn := range regs {
		m := make(map[string]normSite, len(byDyn))
		for dyn, sites := range byDyn {
			var best normSite
			for _, s := range sites {
				if s.line <= 0 {
					continue
				}
				f := relPath(repo.Path, s.file)
				if best.line == 0 || earlierDispatchSite(f, s.line, best.file, best.line) {
					best = normSite{file: f, line: s.line}
				}
			}
			m[dyn] = best
		}
		bestSites[iface] = m
	}

	for ck := range calls {
		ifaceID := interfaceID(ck.iface)
		if ifaceID == "" {
			continue
		}

		candidates := map[string]dispatchCandidate{}
		for dyn := range regs[ck.iface] {
			t := funcs.dynamicTypeOf(dyn)
			if t == nil {
				continue
			}
			fn := findMethodCached(funcs, t, ck.method)
			if fn == nil {
				continue
			}
			key := funcs.candidateKeyOf(fn)
			// R10/todo2-7：同一候选方法可能由多个注册点（值/指针共享方法、
			// 不同文件同号）命中——不能"map 迭代顺序最后者赢"（go2o 同一
			// (ApprovalImpl).IsFinal 的 register_site 在 24/255 间抖动）。
			// 稳定规则：按 (仓库相对文件, 行号) 字典序取最小（所有来源参与
			// 选择；site=0 视为未知不参选）。todo8-6：代表位置已按
			// (接口,动态类型) 预计算。
			best := dispatchCandidate{fn: fn, origin: "register", confidence: 0.9}
			if bs := bestSites[ck.iface][dyn]; bs.line > 0 {
				best.file, best.site = bs.file, bs.line
			}
			if prev, ok := candidates[key]; ok {
				if prev.origin == "register" && best.site > 0 {
					if prev.site == 0 || earlierDispatchSite(best.file, best.site, prev.file, prev.site) {
						prev.file, prev.site = best.file, best.site
						candidates[key] = prev
					}
				}
				continue
			}
			candidates[key] = best
		}
		for _, fn := range pool.methodsFor(ck.iface, ck.method) {
			key := funcs.candidateKeyOf(fn)
			if _, ok := candidates[key]; ok {
				continue
			}
			candidates[key] = dispatchCandidate{fn: fn, origin: "enum", confidence: 0.7}
		}
		for _, c := range candidates {
			id, _, _ := funcIdentity(c.fn)
			if id == "" {
				continue
			}
			meta := map[string]any{
				"interface_method": ck.method,
				"origin":           c.origin,
				"confidence":       c.confidence,
			}
			if c.site > 0 {
				meta["register_site"] = c.site
				if c.file != "" {
					meta["register_file"] = c.file
				}
			}
			if err := emit(domain.Item{Fact: &domain.Fact{
				SourceID:   ifaceID,
				TargetID:   id,
				Kind:       domain.FactDispatchTo,
				ToolSource: domain.ToolSSA,
				Confidence: c.confidence,
				Metadata:   meta,
			}}); err != nil {
				return dispatchPkgs, err
			}
		}
	}
	return dispatchPkgs, nil
}

// earlierDispatchSite 注册点稳定序：(文件, 行号) 字典序最小者优先。
func earlierDispatchSite(f1 string, l1 int, f2 string, l2 int) bool {
	if f1 != f2 {
		return f1 < f2
	}
	return l1 < l2
}
