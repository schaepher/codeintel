package action

// 赋值语句的调用/字段写入展开（从 walkStmt 拆出——行数治理）。

import (
	"go/ast"
	"go/token"
)

func walkSeqAssign(a *Actions, req CodeSequenceRequest, fset *token.FileSet, src []byte,
	s *ast.AssignStmt, callNode func(call *ast.CallExpr) *CodeSeqNode, tgts *seqTargets,
	depth int, recvType string, recvDecl map[string]string, f *ast.File, curPkg, curFile string) []*CodeSeqNode {
	// R100 续：表达式内最外层调用提取（x = foo() + bar()——原只认
	// 顶层 CallExpr，BinaryExpr/Index 等内的调用整条丢失）
	var out []*CodeSeqNode
	// U04y：LHS 接收者调用的求值（`Get().ID = 3` 的 Get 在赋值前执行）。
	for _, lhs := range s.Lhs {
		sel, isSel := lhs.(*ast.SelectorExpr)
		if !isSel {
			continue
		}
		call, isCall := sel.X.(*ast.CallExpr)
		if !isCall {
			continue
		}
		out = append(out, seqArgCallNodes(callNode, call)...)
		if n := callNode(call); n != nil {
			out = append(out, n)
		}
	}
	for _, rhs := range s.Rhs {
		for _, call := range collectCalls(rhs) {
			// T14：链式调用的接收者调用先求值（x := DB.QueryRowContext(...).Scan(&v)
			// 先有 QueryRowContext 再有 Scan），与实参调用同一顺序口径。
			out = append(out, seqArgCallNodes(callNode, call)...)
			if n := callNode(call); n != nil {
				out = append(out, n)
			}
		}
	}
	// todo22 §2：赋值更新函数值环境——在调用节点生成之后（RHS 读旧值，
	// LHS 从下一条语句起生效：`f = wrap(ctx, f)` 的右侧是旧 f）。
	recordSeqAssign(a, req, req.curCtx, s)
	// todo10-2 part3：焦点模式下输出"纯赋值"步骤（状态字段赋值常常
	// 不含调用，纯调用树看不到）；非焦点模式保持原输出不变
	// U04y：字段赋值观察默认保留（assign 节点 + field_writes 证据）；
	// 焦点模式保留原有投影语义（计划投影不顶替分析事实）。
	if !req.focusActive() {
		out = append(out, seqFieldWriteNodes(req, fset, src, s)...)
	}
	if req.focusActive() {
		line := fset.Position(s.Pos()).Line
		node := &CodeSeqNode{Kind: "assign", Label: assignLabel(fset, src, s), Line: line}
		focusHit := req.focusLines != nil && req.focusLines[line]
		if !focusHit && len(req.focus.fieldIDs) == 0 {
			// 局部变量焦点（无类型身份）：按变量名匹配
			focusHit = assignsFocusField(s, req.focus.fields)
		}
		if focusHit {
			node.Focus = true
		}
		out = append(out, node)
	}
	return out
}
