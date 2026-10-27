package action

// todo22（行数治理）：未解析调用点的分类/证据/终点诊断——从
// action_seq_walk_call.go 抽出，避免单函数/单文件超限。

import "go/ast"

// seqUnresolvedCallFallback 未解析到项目内符号：按消费者可区分的类别给出
// 原因（todo13-13），并标注函数值参数逃逸（无法确定是否/何时被调用）。
func seqUnresolvedCallFallback(a *Actions, req CodeSequenceRequest, node *CodeSeqNode,
	f *ast.File, fun ast.Expr, call *ast.CallExpr, curPkg, curFile string) {
	req.acc.unresolved++
	code, detail := classifyUnresolvedCall(f, fun, curPkg, req.pkgSet)
	if esc := seqFuncArgEscape(a, req, call); esc != "" {
		detail += "；" + esc
	}
	node.Evidence = &SeqEvidence{Source: SeqEvidenceAST, CallFile: curFile,
		CallLine: node.Line}
	node.Stop = &SeqStop{Code: code, Detail: detail}
}
