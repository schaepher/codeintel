package action

// todo22：switch/type switch 的语句遍历（从 walkStmt 拆出——行数治理）。
// 语义不变：tag 作为分支节点，每个 case 一个子分支节点。

import (
	"go/ast"
	"go/token"
)

// walkSwitchStmt switch / type switch → 分支节点（case 标签文本 + 子步骤）。
func walkSwitchStmt(a *Actions, req CodeSequenceRequest, fset *token.FileSet, src []byte,
	s ast.Stmt, tgts *seqTargets, depth int, recvType string, recvDecl map[string]string,
	f *ast.File, curPkg, curFile string) []*CodeSeqNode {
	switch sw := s.(type) {
	case *ast.SwitchStmt:
		tag := seqExprText(fset, src, sw.Tag)
		if tag == "" {
			tag = "switch"
		}
		node := &CodeSeqNode{Kind: "branch", Label: tag, Line: fset.Position(sw.Pos()).Line}
		for _, st := range sw.Body.List {
			cc, ok := st.(*ast.CaseClause)
			if !ok {
				continue
			}
			caseNode := &CodeSeqNode{Kind: "branch", Label: "case " + caseExpr(fset, src, cc),
				Line: fset.Position(cc.Pos()).Line}
			caseNode.Nodes = walkStmts(a, req, fset, src, cc.Body, tgts, depth, recvType, recvDecl, f, curPkg, curFile)
			node.Nodes = append(node.Nodes, caseNode)
		}
		return []*CodeSeqNode{node}
	case *ast.TypeSwitchStmt:
		node := &CodeSeqNode{Kind: "branch", Label: "type switch", Line: fset.Position(sw.Pos()).Line}
		for _, st := range sw.Body.List {
			cc, ok := st.(*ast.CaseClause)
			if !ok {
				continue
			}
			caseNode := &CodeSeqNode{Kind: "branch", Label: "case " + caseExpr(fset, src, cc),
				Line: fset.Position(cc.Pos()).Line}
			caseNode.Nodes = walkStmts(a, req, fset, src, cc.Body, tgts, depth, recvType, recvDecl, f, curPkg, curFile)
			node.Nodes = append(node.Nodes, caseNode)
		}
		return []*CodeSeqNode{node}
	}
	return nil
}
