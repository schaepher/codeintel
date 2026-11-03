package action

// 声明语句（`var ...`）的调用节点展开（U04h：声明值里的写入也建节点）。

import (
	"go/ast"
	"go/token"
)

func walkSeqDeclStmt(a *Actions, req CodeSequenceRequest, fset *token.FileSet, src []byte,
	s *ast.DeclStmt, callNode func(call *ast.CallExpr) *CodeSeqNode) []*CodeSeqNode {
	gd, ok := s.Decl.(*ast.GenDecl)
	if !ok {
		return nil
	}
	// todo22 §2：`var f = func(){…}` / `var f func()`——只建值，不执行
	recordSeqDecl(a, req, req.curCtx, gd)
	var out []*CodeSeqNode
	for _, spec := range gd.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for _, v := range vs.Values {
			for _, call := range collectCalls(v) {
				out = append(out, seqArgCallNodes(callNode, call)...)
				if n := callNode(call); n != nil {
					out = append(out, n)
				}
			}
		}
	}
	return out
}
