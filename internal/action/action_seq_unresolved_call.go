package action

// 未解析调用的展开/分类（从 newSeqCallNode 拆出——行数治理）。

import "go/ast"

// seqExpandUnresolvedCall 未解析到项目内符号的调用：
//   - 外部依赖按需源码解析（todo22 §4.2：depth>1 才 go list；离线只读、
//     请求内缓存；展开成功时按外部函数自己的形参重新绑定回调）；
//   - 外部方法调用；
//   - 仍不可解析 → 分类诊断（todo13-13/todo12-13，含函数值实参逃逸）。
//
// 返回 true 表示已展开（调用方直接返回节点）。
func seqExpandUnresolvedCall(a *Actions, req CodeSequenceRequest, node *CodeSeqNode,
	f *ast.File, fun ast.Expr, call *ast.CallExpr, depth int, curPkg, curFile string, line int) bool {
	if seqExpandExternalCall(a, req, node, f, fun, call, depth, curFile, line) {
		return true
	}
	// T10：外部包源码内部的同包函数调用（裸标识符）——按当前外部包上下文展开。
	if importPath, declName, extID, ok := seqExternalSamePkgCall(a, req, fun); ok {
		if seqExpandExternalDecl(a, req, node, importPath, declName, extID, depth, call, curFile, line, "") {
			return true
		}
	}
	if seqExpandExternalMethodCall(a, req, node, call, depth, curFile, line) {
		return true
	}
	seqUnresolvedCallFallback(a, req, node, f, fun, call, curPkg, curFile)
	return false
}
