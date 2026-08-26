package action

// R95 迁移：代码级时序的 AST 文本辅助（原 cli/query_sequence_code.go
// callLabel/callActor/caseExpr/exprText——exprText 与 conditions.go
// 的签名冲突，改名 seqExprText）。

import (
	"go/ast"
	"go/token"
	"strings"
)

// caseExpr case 子句条件文本（多值逗号连接；default 为空）。
func caseExpr(fset *token.FileSet, src []byte, cc *ast.CaseClause) string {
	if len(cc.List) == 0 {
		return "default"
	}
	var parts []string
	for _, e := range cc.List {
		parts = append(parts, seqExprText(fset, src, e))
	}
	return strings.Join(parts, ", ")
}

// callLabel 调用目标文本（obj.Method / fn() / (T).m——源码形态）。
func callLabel(fset *token.FileSet, src []byte, fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return seqExprText(fset, src, f.X) + "." + f.Sel.Name
	default:
		return seqExprText(fset, src, fun)
	}
}

// callActor 调用参与者（R83：对象而非方法）——s.manager.SubmitOrder →
// s.manager；t.repo.CreateOrder → t.repo；ic.Put → ic；链式
// s.memberRepo.GetMember(...).GetAccount → s.memberRepo（取调用链
// receiver）；纯函数（Ident/其他）→ 调用名本身。
func callActor(fset *token.FileSet, src []byte, fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.SelectorExpr:
		// 链式调用（X 是调用）：取被调方法的 receiver（GetMember 的
		// receiver = s.memberRepo）
		if call, ok := f.X.(*ast.CallExpr); ok {
			if sel, ok2 := call.Fun.(*ast.SelectorExpr); ok2 {
				return seqExprText(fset, src, sel.X)
			}
		}
		return seqExprText(fset, src, f.X)
	case *ast.Ident:
		return f.Name
	default:
		return callLabel(fset, src, fun)
	}
}

// seqExprText 表达式源码文本（fset 位置取原文——保持源码形态；
// 与 conditions.go 的 exprText（format.Node 重排版）区分）。
func seqExprText(fset *token.FileSet, src []byte, e ast.Expr) string {
	if e == nil {
		return ""
	}
	start := fset.Position(e.Pos()).Offset
	end := fset.Position(e.End()).Offset
	if start < 0 || end > len(src) || start >= end {
		return ""
	}
	s := string(src[start:end])
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}
