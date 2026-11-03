package action

// select 多路复用的时序展开（从 walkStmt 拆出——行数治理）。

import (
	"go/ast"
	"go/token"
)

func walkSeqSelectStmt(a *Actions, req CodeSequenceRequest, fset *token.FileSet, src []byte,
	s *ast.SelectStmt, tgts *seqTargets, depth int, recvType string, recvDecl map[string]string,
	f *ast.File, curPkg, curFile string,
	callNode func(call *ast.CallExpr) *CodeSeqNode) []*CodeSeqNode {
	node := &CodeSeqNode{Kind: "branch", Label: "select", Line: fset.Position(s.Pos()).Line}
	for _, st := range s.Body.List {
		if cc, ok := st.(*ast.CommClause); ok {
			caseLabel := "case "
			if cc.Comm != nil {
				caseLabel += commExprText(fset, src, cc.Comm)
			} else {
				caseLabel += "default"
			}
			caseNode := &CodeSeqNode{Kind: "branch", Label: caseLabel, Line: fset.Position(cc.Pos()).Line}
			// Comm 通信表达式内的调用（case ch2 <- svc.Get() 的 Get
			// ——send 分支调用在 Comm 不在 Body）
			var commExprs []ast.Expr
			switch comm := cc.Comm.(type) {
			case *ast.SendStmt:
				commExprs = []ast.Expr{comm.Value}
			case *ast.AssignStmt:
				commExprs = comm.Rhs
			case *ast.ExprStmt:
				commExprs = []ast.Expr{comm.X}
			}
			for _, e := range commExprs {
				for _, call := range collectCalls(e) {
					caseNode.Nodes = append(caseNode.Nodes, seqArgCallNodes(callNode, call)...)
					if n := callNode(call); n != nil {
						caseNode.Nodes = append(caseNode.Nodes, n)
					}
				}
			}
			caseNode.Nodes = append(caseNode.Nodes, walkStmts(a, req, fset, src, cc.Body, tgts, depth, recvType, recvDecl, f, curPkg, curFile)...)
			node.Nodes = append(node.Nodes, caseNode)
		}
	}
	return []*CodeSeqNode{node}
}
