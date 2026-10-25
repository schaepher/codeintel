package action

// todo10-2/10-3：调用节点构造（含接口/字段/局部变量具体化与嵌套展开）
// 从 walkStmt 的闭包提升为顶层函数——行数治理（walkStmt 曾超 300 行），
// 逻辑逐行一致。

import (
	"go/ast"
	"go/token"
	"strings"
)

// newSeqCallNode 返回"位置 → 调用节点"的构造函数（闭包捕获遍历上下文，
// 保持 walkStmt 的调用点写法不变）。
func newSeqCallNode(a *Actions, req CodeSequenceRequest, fset *token.FileSet, src []byte,
	tgts *seqTargets, depth int, recvType string, recvDecl map[string]string,
	f *ast.File, curPkg, curFile string) func(fun ast.Expr, pos token.Position) *CodeSeqNode {
	return func(fun ast.Expr, pos token.Position) *CodeSeqNode {
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
			// todo10-2 part3：被调者写焦点字段 → 该调用点命中
			if req.focus != nil && req.focus.writers[tid] {
				node.Focus = true
			}
			// todo10-9：有可解析的目标却因 depth 上限未展开 → 记"depth 截断"
			// （终止原因不是"流程结束"）
			if depth <= 1 {
				req.acc.depth = true
			}
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
}
