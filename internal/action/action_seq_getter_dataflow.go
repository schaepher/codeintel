package action

// T04/U06：getter/构造器返回值的接口具体化（`Get().Work()`）。
//
// 证明链：内层无参调用 `Get()` 的返回来源（构造器实际 return / 包级单例写入
// + init 调用实参 / 接收者字段）必须是**唯一具体类型**；未知写者或多个互斥
// 来源时保留接口身份、标 unknown_receiver（不借全库唯一实现）。

import (
	"go/ast"
	"go/token"

	"github.com/schaepher/codeintel/internal/domain"
)

// seqExpandGetterIfaceCall 处理 `X().Method()`（X 为包级函数/工厂）的接口
// 具体化。返回 true = 已处理（成功具体化或明确未知）。
func seqExpandGetterIfaceCall(a *Actions, req CodeSequenceRequest, node *CodeSeqNode,
	fun ast.Expr, f *ast.File, curPkg string, depth int, curFile string, pos token.Position) bool {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	inner, ok := sel.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	if funSel, isSel := inner.Fun.(*ast.SelectorExpr); isSel {
		// 方法 getter（b.Get()）走工厂字段/数据流路径；**包级函数** getter
		// （region.Get()，U07 单例）继续走这里。
		id, ok := funSel.X.(*ast.Ident)
		if !ok || !seqIsPkgIdent(f, id) {
			return false
		}
	}
	// U07：单例 getter（无参，返回包级变量）+ 条件工厂（switch 选实现）→
	// 候选带工厂守卫（保留原调用参与者）。
	if len(inner.Args) == 0 {
		if seqExpandSingletonGetterCall(a, req, node, fun, f, curPkg, depth, curFile, pos) {
			return true
		}
	}
	// T04：有实参的工厂调用（`FromField(kind).Work()`）按守卫裁剪 return；
	// 无法按工厂解析时退回声明返回类型（无参 getter 的旧路径）。
	var types []writeSource
	if len(inner.Args) > 0 {
		types, _ = seqFactoryCallReturnSources(a, req, f, inner, 3, curPkg)
	} else {
		types = callReturnTypes(a, req, f, importAliases(f), inner.Fun, 3, curPkg)
	}
	if len(types) == 0 {
		return false
	}
	unknown := hasUnknownWriteSource(types)
	types = knownWriteSources(types)
	method := sel.Sel.Name
	var impls []string
	for _, t := range types {
		if t.typeName == "" || t.pkgPath == "" {
			continue
		}
		impl := "symbol:go:" + t.pkgPath + ":(" + t.typeName + ")." + method
		if n, err := a.repo.GetSymbol(domain.CanonicalID(impl)); err == nil && n != nil {
			impls = append(impls, impl)
		}
	}
	if unknown || len(impls) == 0 {
		markSeqUnknownGetter(node, curFile, pos.Line, "getter 返回来源不完整（存在未知写者/无具体返回值）")
		return true
	}
	if len(impls) > 1 {
		node.Evidence = &SeqEvidence{Source: SeqEvidenceFieldFlow, CallFile: curFile, CallLine: pos.Line,
			Candidates: impls, Ambiguous: true, Reason: "implementation-alternatives",
			CandidateBudget: seqIfaceExpandBudget(len(impls), len(impls), false)}
		node.Stop = &SeqStop{Code: SeqStopIfaceAmbiguous,
			Detail: "getter 返回多个互斥具体来源；候选分支不是已确定执行顺序"}
		capped, truncated := capSeqCandidates(impls)
		seqExpandCandidateBranches(a, req, node, capped, depth, pos, truncated, len(impls))
		return true
	}
	impl := impls[0]
	if a.isIfaceMethodID(impl) {
		markSeqUnknownGetter(node, curFile, pos.Line, "getter 只证明了接口类型（缺实例来源）")
		return true
	}
	node.ImplType = implTypeShort(impl)
	node.Evidence = &SeqEvidence{Source: SeqEvidenceFieldFlow, CallFile: curFile, CallLine: pos.Line,
		SymbolID: impl, Confidence: 1.0, Reason: "getter-dataflow",
		Verdict: SeqVerdictCorroborated}
	seqExpandInto(a, req, node, impl, depth)
	return true
}

// markSeqUnknownGetter 标记 getter 返回值来源未知（保留接口调用、不展开实现）。
func markSeqUnknownGetter(node *CodeSeqNode, curFile string, line int, detail string) {
	node.Evidence = &SeqEvidence{Source: SeqEvidenceFieldFlow, CallFile: curFile, CallLine: line,
		Reason: "unknown_receiver", Ambiguous: true}
	node.Stop = &SeqStop{Code: SeqStopUnknownReceiver, Detail: detail}
}

// seqFactoryCallReturnSources 调用点 `F(args).M()` 的工厂返回来源：
// F 的每个 return 按守卫裁剪（调用点实参已知时只取可达 return），
// RHS 用 rhsTypes 解析具体类型；未知/nil return 追加未知哨兵（T04：
// 任一未知 return 使"唯一具体来源"失效）。
//
// ok=false 表示不是可解析的本仓工厂调用（交给其它路径/保持接口身份）。
func seqFactoryCallReturnSources(a *Actions, req CodeSequenceRequest, f *ast.File,
	call *ast.CallExpr, depth int, curPkg string) ([]writeSource, bool) {
	if call == nil || f == nil {
		return nil, false
	}
	target, ok := seqASTCallTarget(call.Fun, f, curPkg)
	if !ok {
		return nil, false
	}
	ctx, fd, ok := loadSeqFuncDecl(a, req, target)
	if !ok || fd == nil || fd.Body == nil {
		return nil, false
	}
	consts := seqConstValues(a, req, ctx.rel)
	args := seqCalleeArgLiterals(fd, call, consts)
	imports := importAliases(ctx.file)
	pkg := pkgOfID(domain.CanonicalID(target))
	var out []writeSource
	for _, site := range seqReturnSitesOf(fd) {
		if !seqReturnGuardsAllow(site.guards, args, consts) {
			continue
		}
		for _, r := range site.exprs {
			srcs := rhsTypes(a, req, ctx.file, imports, r, depth, pkg)
			if len(srcs) == 0 {
				out = appendUnique(out, seqUnknownWriteSource())
				continue
			}
			// T04：来源退回到"声明接口"（`return nil` 之类的声明类型回退）
			// 不是具体实例——按未知处理，不能当唯一具体来源。
			known := knownWriteSources(srcs)
			if len(known) == 1 && a.isIfaceTypeName(req, known[0].typeName) {
				out = appendUnique(out, seqUnknownWriteSource())
				continue
			}
			out = appendUnique(out, srcs...)
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}
