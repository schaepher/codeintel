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
		// R100 续：表达式内最外层调用提取（x = foo() + bar()——原只认
		// 顶层 CallExpr，BinaryExpr/Index 等内的调用整条丢失）
		var out []*CodeSeqNode
		for _, rhs := range s.Rhs {
			for _, call := range collectCalls(rhs) {
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
		// R100 续：return foo() + 1——表达式内调用提取（原只认顶层
		// CallExpr，BinaryExpr 内的调用只画 return 线、调用丢失）
		var out []*CodeSeqNode
		for _, r := range s.Results {
			for _, call := range collectCalls(r) {
				if n := callNode(call.Fun, fset.Position(call.Lparen)); n != nil {
					out = append(out, n)
				}
			}
		}

		if len(out) == 0 {
			out = append(out, &CodeSeqNode{Kind: "return", Label: "return", Line: fset.Position(s.Pos()).Line})
		}
		return out
	case *ast.SendStmt:
		// R100 续：ch <- foo()——原无 case 整条丢弃；提取发送值内调用
		//（纯值发送 ch <- 5 无调用可画——发送是通信动作，无消息线）
		var out []*CodeSeqNode
		for _, call := range collectCalls(s.Value) {
			if n := callNode(call.Fun, fset.Position(call.Lparen)); n != nil {
				out = append(out, n)
			}
		}
		return out
	case *ast.SelectStmt:
		// R100 续：select 多路复用——原无 case 整块丢弃；仿 SwitchStmt
		// 分支结构（case 内调用 + send 分支提取）
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
						if n := callNode(call.Fun, fset.Position(call.Lparen)); n != nil {
							caseNode.Nodes = append(caseNode.Nodes, n)
						}
					}
				}
				caseNode.Nodes = append(caseNode.Nodes, walkStmts(a, req, fset, src, cc.Body, tgts, depth, recvType, recvDecl, f, curPkg, curFile)...)
				node.Nodes = append(node.Nodes, caseNode)
			}
		}
		return []*CodeSeqNode{node}
	case *ast.LabeledStmt:
		// R100 续：Loop: for——原无 case 整块丢弃（label 本身无时序
		// 语义，递归内部语句）
		return walkStmt(a, req, fset, src, s.Stmt, tgts, depth, recvType, recvDecl, f, curPkg, curFile)
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
