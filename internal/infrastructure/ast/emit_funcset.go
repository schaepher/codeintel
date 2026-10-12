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
		iid, ikind := fnID(ic)
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
func (ctx *fileCtx) emitFuncSetRegistry(call *ast.CallExpr, callee *types.Func, calleeID domain.CanonicalID, calleeKind domain.EntityKind) {
	sig, _ := callee.Type().(*types.Signature)
	if sig == nil || !tupleHasFuncSet(sig.Params()) || !tupleHasFuncSet(sig.Results()) {
		return
	}
	for _, arg := range call.Args {
		if !isFuncSetType(ctx.pkg.TypesInfo.TypeOf(arg)) {
			continue
		}
		for _, fn := range collectFuncSetRefs(ctx.pkg, arg) {
			if fn.Pkg() == nil || !isInModule(fn.Pkg().Path(), ctx.repo.Modules) {
				continue
			}
			fid, fkind := fnID(fn)
			if fid == "" || fid == calleeID {
				continue
			}
			_ = ctx.emit(domain.Item{Node: nodeFor(ctx.repo, ctx.pkg, callee, calleeID, calleeKind, nil)})
			_ = ctx.emit(domain.Item{Node: nodeFor(ctx.repo, ctx.pkg, fn, fid, fkind, nil)})
			_ = ctx.emit(domain.Item{Fact: &domain.Fact{
				SourceID: calleeID, TargetID: fid, Kind: domain.FactCalls,
				ToolSource: domain.ToolCodeGraph, Confidence: 0.8,
				Metadata: map[string]any{"func_registry": true},
			}})
		}
	}
}
