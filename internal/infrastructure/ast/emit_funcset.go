package ast

// 函数集合（func-set）相关调用边（从 emitcall.go 拆出——行数治理）：
// 变参/append 嵌套 getter 调用补 CALLS 边 + "函数集合入参、函数集合
// 返回"的 getter 到注册函数的默认可见 CALLS 边。

import (
	"go/ast"
	"go/types"

	"github.com/schaepher/codeintel/internal/domain"
)

// emitFuncSetNestedCalls 函数集合嵌套调用补边：内建 append(list, g()...)
// 或变参展开 outer(g()...) 中，内层 g() 返回函数集合——除返回值关系
// （handleNestedArg 的 passes_result）外，补 caller → g 的 CALLS 边
// （修复 region getter 到上层工厂的断链）。内建 append 无 *types.Func，
// 到不了 handleNestedArg，故在此单独处理。
func (ctx *fileCtx) emitFuncSetNestedCalls(call *ast.CallExpr, caller *types.Func, callerID domain.CanonicalID, callerKind domain.EntityKind) {
	if !isFuncSetConsumer(call) {
		return
	}
	for _, arg := range call.Args {
		inner, isCall := arg.(*ast.CallExpr)
		if !isCall {
			continue
		}
		if !isFuncSetType(ctx.pkg.TypesInfo.TypeOf(inner)) {
			continue
		}
		ic, ok := resolveCallee(ctx.pkg.TypesInfo, inner.Fun)
		if !ok || ic.Pkg() == nil || !isInModule(ic.Pkg().Path(), ctx.repo.Modules) {
			continue
		}
		iid, ikind := ctx.a.fnIDOf(ic)
		if iid == "" || iid == callerID {
			continue
		}
		_ = ctx.emit(domain.Item{Node: nodeFor(ctx.repo, ctx.pkg, caller, callerID, callerKind, ctx.serviceFlags[callerID])})
		_ = ctx.emit(domain.Item{Node: nodeFor(ctx.repo, ctx.pkg, ic, iid, ikind, nil)})
		_ = ctx.emit(domain.Item{Fact: &domain.Fact{
			SourceID: callerID, TargetID: iid, Kind: domain.FactCalls,
			ToolSource: domain.ToolCodeGraph, Confidence: 0.8,
			Metadata: map[string]any{
				"line_num": ctx.pkg.Fset.PositionFor(call.Pos(), false).Line,
				"func_set": true,
			},
		}})
	}
}

// isFuncSetConsumer 调用点是否消费函数集合：内建 append 或变参展开
// （call.Ellipsis 有效）。其他调用不在本机制范围（避免误建 calls）。
func isFuncSetConsumer(call *ast.CallExpr) bool {
	if call.Ellipsis.IsValid() {
		return true
	}
	if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "append" {
		return true
	}
	return false
}

// emitFuncSetRegistry 函数集合注册表传播：callee 形参含函数集合、返回
// 也含函数集合（"函数集合入参、函数集合返回"的 getter）时，为 callee →
// 实参里注册的每个函数补 CALLS 边（默认可见；不按签名猜测全部候选函数）。
// 注册函数从复合字面量（含嵌套字段）收集，访问集防自引用类型图递归。
func (ctx *fileCtx) emitFuncSetRegistry(call *ast.CallExpr, caller *types.Func, callerID domain.CanonicalID, callerKind domain.EntityKind, callee *types.Func, calleeID domain.CanonicalID, calleeKind domain.EntityKind) {
	sig, _ := callee.Type().(*types.Signature)
	if sig == nil || !tupleHasFuncSet(sig.Params()) || !tupleHasFuncSet(sig.Results()) {
		return
	}
	line := ctx.pkg.Fset.PositionFor(call.Pos(), false).Line
	for _, arg := range call.Args {
		if !isFuncSetType(ctx.pkg.TypesInfo.TypeOf(arg)) {
			continue
		}
		// 内联复合字面量 + 包级注册表变量（emit_registered_funcval.go）
		refs := collectFuncSetRefs(ctx.pkg, arg)
		refs = append(refs, ctx.registryFuncRefs(arg)...)
		for _, fn := range dedupSortFuncs(refs) {
			if fn.Pkg() == nil || !isInModule(fn.Pkg().Path(), ctx.repo.Modules) {
				continue
			}
			fid, fkind := ctx.a.fnIDOf(fn)
			if fid == "" || fid == calleeID {
				continue
			}
			_ = ctx.emit(domain.Item{Node: nodeFor(ctx.repo, ctx.pkg, callee, calleeID, calleeKind, nil)})
			_ = ctx.emit(domain.Item{Node: nodeFor(ctx.repo, ctx.pkg, fn, fid, fkind, nil)})
			// getter → 注册函数（changelog2）
			_ = ctx.emit(domain.Item{Fact: &domain.Fact{
				SourceID: calleeID, TargetID: fid, Kind: domain.FactCalls,
				ToolSource: domain.ToolCodeGraph, Confidence: 0.8,
				Metadata: map[string]any{"func_registry": true},
			}})
			// 当前调用者 → 注册函数（changelog3：集合参数传入 getter 时，
			// 调用者也是注册函数的可见调用方；带行号与 registered_function_value 标记）
			if callerID != "" && callerID != fid {
				_ = ctx.emit(domain.Item{Node: nodeFor(ctx.repo, ctx.pkg, caller, callerID, callerKind, ctx.serviceFlags[callerID])})
				_ = ctx.emit(domain.Item{Fact: &domain.Fact{
					SourceID: callerID, TargetID: fid, Kind: domain.FactCalls,
					ToolSource: domain.ToolCodeGraph, Confidence: 0.8,
					Metadata: map[string]any{
						"line_num":                  line,
						"registered_function_value": true,
					},
				}})
			}
		}
	}
}

// enclosingRange 从 ast 栈里找最近的外层 for-range（不跨函数边界）。
func enclosingRange(stack []ast.Node) *ast.RangeStmt {
	for i := len(stack) - 1; i >= 0; i-- {
		if rs, ok := stack[i].(*ast.RangeStmt); ok {
			return rs
		}
		switch stack[i].(type) {
		case *ast.FuncDecl, *ast.FuncLit:
			return nil
		}
	}
	return nil
}

// emitFuncSetTraversalCall 遍历函数集合注册表并执行其中函数：
//
//	for _, v := range registry { v.F() }
//
// → 遍历函数 → 每个注册函数 CALLS 边（置信度 0.8，带
// registered_function_value + 行号）——上层查询能识别"谁遍历执行了注册集合"。
// 仅当 range 表达式是函数集合、且被调用字段的基就是 range 值变量时触发。
func (ctx *fileCtx) emitFuncSetTraversalCall(call *ast.CallExpr, caller *types.Func, callerID domain.CanonicalID, callerKind domain.EntityKind) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	base, ok := sel.X.(*ast.Ident)
	if !ok || base == nil {
		return
	}
	rs := enclosingRange(ctx.stack)
	if rs == nil || !isFuncSetType(ctx.pkg.TypesInfo.TypeOf(rs.X)) {
		return
	}
	rv, ok := rs.Value.(*ast.Ident)
	if !ok || rv.Name != base.Name {
		return
	}
	refs := collectFuncSetRefs(ctx.pkg, rs.X)
	refs = append(refs, ctx.registryFuncRefs(rs.X)...)
	line := ctx.pkg.Fset.PositionFor(call.Pos(), false).Line
	for _, fn := range dedupSortFuncs(refs) {
		if fn.Pkg() == nil || !isInModule(fn.Pkg().Path(), ctx.repo.Modules) {
			continue
		}
		fid, fkind := ctx.a.fnIDOf(fn)
		if fid == "" || fid == callerID {
			continue
		}
		_ = ctx.emit(domain.Item{Node: nodeFor(ctx.repo, ctx.pkg, caller, callerID, callerKind, ctx.serviceFlags[callerID])})
		_ = ctx.emit(domain.Item{Node: nodeFor(ctx.repo, ctx.pkg, fn, fid, fkind, nil)})
		_ = ctx.emit(domain.Item{Fact: &domain.Fact{
			SourceID: callerID, TargetID: fid, Kind: domain.FactCalls,
			ToolSource: domain.ToolCodeGraph, Confidence: 0.8,
			Metadata: map[string]any{
				"line_num":                  line,
				"registered_function_value": true,
			},
		}})
	}
}
