package action

import (
	"go/ast"
	"go/token"
	"strings"
)

// walkStmts 遍历语句列表生成步骤（调用语句 / 分支 / 循环 / 块递归）。
// f = 当前函数所在文件（局部变量 DI 注入数据流查询用）；curPkg = 当前
// 函数包路径（P0-6）。tgts = 调用点 → 被调符号映射（R99-3：offset
// 优先——同行多调用区分）。
func walkStmts(a *Actions, req CodeSequenceRequest, fset *token.FileSet, src []byte, stmts []ast.Stmt, tgts *seqTargets, depth int, recvType string, recvDecl map[string]string, f *ast.File, curPkg, curFile string) []*CodeSeqNode {
	var out []*CodeSeqNode
	for _, st := range stmts {
		out = append(out, walkStmt(a, req, fset, src, st, tgts, depth, recvType, recvDecl, f, curPkg, curFile)...)
	}
	return out
}

// walkStmt 单条语句 → 步骤（可能多条：赋值内多个调用）。
func walkStmt(a *Actions, req CodeSequenceRequest, fset *token.FileSet, src []byte, stmt ast.Stmt, tgts *seqTargets, depth int, recvType string, recvDecl map[string]string, f *ast.File, curPkg, curFile string) []*CodeSeqNode {
	callNode := func(fun ast.Expr, pos token.Position) *CodeSeqNode {
		node := &CodeSeqNode{Kind: "call", Label: callLabel(fset, src, fun), Actor: callActor(fset, src, fun), Line: pos.Line}

		if tid, ok := tgts.lookup(pos); ok {

			if seqFilterHit(req.Filter, tid, curFile, node.Label) {
				return nil
			}
			sigID := tid
			if !strings.Contains(tid, ":(") {
				if sel, isSel := fun.(*ast.SelectorExpr); isSel {
					sigID = GrpcMethodEntryID(tid, sel.Sel.Name)
				}
			}
			node.Type = implTypeShort(tid)
			if args, rets, ok2 := sigTypesOf(a, sigID); ok2 {
				node.Args, node.Returns = args, rets
			}
		}

		if depth > 1 {
			if tid, ok := tgts.lookup(pos); ok {

				if seqStopPkgHit(tid, req.StopPackages) {
					return node
				}

				if sel, isSel := fun.(*ast.SelectorExpr); isSel && recvType != "" {
					if field, ok2 := receiverFieldSel(sel, recvType); ok2 {
						if implMethod := a.receiverFieldImpl(req, recvType, field, sel.Sel.Name); implMethod != "" {

							node.ImplType = implTypeShort(implMethod)
							if decl, ok := recvDecl[field]; ok {
								node.DeclType = decl
							}
							if child := codeSeqForSymbol(a, req, implMethod, depth-1); child != nil {
								node.Nodes = child.Nodes
								return node
							}
						}
					}
				}

				if sel, isSel := fun.(*ast.SelectorExpr); isSel {
					if id, ok := sel.X.(*ast.Ident); ok {
						if implMethod := a.localVarImpl(req, f, curPkg, id.Name, sel.Sel.Name); implMethod != "" {
							node.ImplType = implTypeShort(implMethod)
							if child := codeSeqForSymbol(a, req, implMethod, depth-1); child != nil {
								node.Nodes = child.Nodes
								return node
							}
						}
					}
				}

				if strings.Contains(tid, ":(") {
					if impl, ok := a.InterfaceMethodImpl(tid); ok {
						node.ImplType = implTypeShort(impl)
						if child := codeSeqForSymbol(a, req, impl, depth-1); child != nil {
							node.Nodes = child.Nodes
							return node
						}
					}
				}
				if !strings.Contains(tid, ":(") {
					if sel, isSel := fun.(*ast.SelectorExpr); isSel {
						if child := codeSeqForSymbol(a, req, GrpcMethodEntryID(tid, sel.Sel.Name), depth-1); child != nil {
							node.Nodes = child.Nodes
							return node
						}
					}
				}
				if child := codeSeqForSymbol(a, req, tid, depth-1); child != nil {
					node.Nodes = child.Nodes
				}
			}
		}
		return node
	}
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		if call, ok := s.X.(*ast.CallExpr); ok {
			if n := callNode(call.Fun, fset.Position(call.Lparen)); n != nil {
				return []*CodeSeqNode{n}
			}
			return nil
		}
	case *ast.AssignStmt:
		var out []*CodeSeqNode
		for _, rhs := range s.Rhs {
			if call, ok := rhs.(*ast.CallExpr); ok {
				if n := callNode(call.Fun, fset.Position(call.Lparen)); n != nil {
					out = append(out, n)
				}
			}
		}
		return out
	case *ast.IfStmt:
		var steps []*CodeSeqNode

		if s.Init != nil {
			steps = append(steps, walkStmt(a, req, fset, src, s.Init, tgts, depth, recvType, recvDecl, f, curPkg, curFile)...)
		}
		node := &CodeSeqNode{Kind: "branch", Label: seqExprText(fset, src, s.Cond), Line: fset.Position(s.Cond.Pos()).Line}
		node.Nodes = walkStmts(a, req, fset, src, s.Body.List, tgts, depth, recvType, recvDecl, f, curPkg, curFile)
		if blk, ok := s.Else.(*ast.BlockStmt); ok {
			node.Else = walkStmts(a, req, fset, src, blk.List, tgts, depth, recvType, recvDecl, f, curPkg, curFile)
		} else if elseIf, ok := s.Else.(*ast.IfStmt); ok {
			node.Else = walkStmt(a, req, fset, src, elseIf, tgts, depth, recvType, recvDecl, f, curPkg, curFile)
		}
		steps = append(steps, node)
		return steps
	case *ast.ForStmt:
		label := seqExprText(fset, src, s.Cond)
		if label == "" {
			label = "无限循环"
		}
		return []*CodeSeqNode{{Kind: "loop", Label: label, Line: fset.Position(s.Pos()).Line,
			Nodes: walkStmts(a, req, fset, src, s.Body.List, tgts, depth, recvType, recvDecl, f, curPkg, curFile)}}
	case *ast.RangeStmt:
		return []*CodeSeqNode{{Kind: "loop", Label: "range " + seqExprText(fset, src, s.X), Line: fset.Position(s.Pos()).Line,
			Nodes: walkStmts(a, req, fset, src, s.Body.List, tgts, depth, recvType, recvDecl, f, curPkg, curFile)}}
	case *ast.BlockStmt:
		return walkStmts(a, req, fset, src, s.List, tgts, depth, recvType, recvDecl, f, curPkg, curFile)
	case *ast.DeferStmt:
		if n := callNode(s.Call.Fun, fset.Position(s.Call.Lparen)); n != nil {
			return []*CodeSeqNode{n}
		}
		return nil
	case *ast.GoStmt:
		if n := callNode(s.Call.Fun, fset.Position(s.Call.Lparen)); n != nil {
			return []*CodeSeqNode{n}
		}
		return nil
	case *ast.ReturnStmt:
		var out []*CodeSeqNode
		for _, r := range s.Results {
			if call, ok := r.(*ast.CallExpr); ok {
				if n := callNode(call.Fun, fset.Position(call.Lparen)); n != nil {
					out = append(out, n)
				}
			}
		}

		if len(out) == 0 {
			out = append(out, &CodeSeqNode{Kind: "return", Label: "return", Line: fset.Position(s.Pos()).Line})
		}
		return out
	case *ast.BranchStmt:

		kind := s.Tok.String()
		return []*CodeSeqNode{{Kind: kind, Label: kind, Line: fset.Position(s.Pos()).Line}}
	case *ast.SwitchStmt:

		tag := seqExprText(fset, src, s.Tag)
		if tag == "" {
			tag = "switch"
		}
		node := &CodeSeqNode{Kind: "branch", Label: tag, Line: fset.Position(s.Pos()).Line}
		for _, st := range s.Body.List {
			if cc, ok := st.(*ast.CaseClause); ok {
				caseNode := &CodeSeqNode{Kind: "branch", Label: "case " + caseExpr(fset, src, cc), Line: fset.Position(cc.Pos()).Line}
				caseNode.Nodes = walkStmts(a, req, fset, src, cc.Body, tgts, depth, recvType, recvDecl, f, curPkg, curFile)
				node.Nodes = append(node.Nodes, caseNode)
			}
		}
		return []*CodeSeqNode{node}
	case *ast.TypeSwitchStmt:
		node := &CodeSeqNode{Kind: "branch", Label: "type switch", Line: fset.Position(s.Pos()).Line}
		for _, st := range s.Body.List {
			if cc, ok := st.(*ast.CaseClause); ok {
				caseNode := &CodeSeqNode{Kind: "branch", Label: "case " + caseExpr(fset, src, cc), Line: fset.Position(cc.Pos()).Line}
				caseNode.Nodes = walkStmts(a, req, fset, src, cc.Body, tgts, depth, recvType, recvDecl, f, curPkg, curFile)
				node.Nodes = append(node.Nodes, caseNode)
			}
		}
		return []*CodeSeqNode{node}
	}
	return nil
}
