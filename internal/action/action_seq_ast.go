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

// commExprText select case 通信表达式文本（R100 续——Comm 是
// ast.Stmt 包装：ExprStmt（<-ch）/ SendStmt（ch2 <- x）/ AssignStmt
// （v := <-ch，取 RHS 接收表达式））。
func commExprText(fset *token.FileSet, src []byte, comm ast.Stmt) string {
	switch c := comm.(type) {
	case *ast.ExprStmt:
		return seqExprText(fset, src, c.X)
	case *ast.SendStmt:
		return seqExprText(fset, src, c.Chan) + " <- " + seqExprText(fset, src, c.Value)
	case *ast.AssignStmt:
		if len(c.Rhs) == 1 {
			return seqExprText(fset, src, c.Rhs[0])
		}
	}
	return ""
}

// collectCalls 收集表达式内最外层调用（R100 续：return foo() + 1 /
// x = foo() + bar() 的调用提取）。参数位调用不提取（foo(bar()) 只收
// foo——与 AST 适配器 isArgCall 语义一致）；链式调用只收最外层
// （a.foo().bar() 收 bar——时序图上链的终点调用）。
func collectCalls(expr ast.Expr) []*ast.CallExpr {
	if expr == nil {
		return nil
	}
	var out []*ast.CallExpr
	ast.Inspect(expr, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		out = append(out, call)
		return false // 不深入调用内部（参数位/链式内层调用跳过）
	})
	return out
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
	// S3：换行/制表符统一换空格——多行条件合并成一行（mermaid label
	// 含 tab 渲染失败）；字符串字面量内的 \t 转义序列（反斜杠+t）不
	// 含真实 tab 字符，不受影响
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	return strings.TrimSpace(s)
}
