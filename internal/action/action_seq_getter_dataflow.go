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
	if !ok || len(inner.Args) > 0 {
		return false
	}
	if _, isSel := inner.Fun.(*ast.SelectorExpr); isSel {
		return false // 方法 getter（b.Get()）走工厂字段/数据流路径
	}
	types := callReturnTypes(a, req, f, importAliases(f), inner.Fun, 3, curPkg)
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
		seqExpandCandidateBranches(a, req, node, impls, depth, pos)
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
