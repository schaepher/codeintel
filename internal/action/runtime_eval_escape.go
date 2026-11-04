package action

// T14b：运行时取址逃逸（未建模指针别名，按求值位置持续失效）。

import (
	"go/ast"
	"go/token"
)

// markAddressEscape 取址的根对象标识符（括号/字段/下标取址至少失效根）。
func (e *rtEval) markAddressEscape(expr ast.Expr, s *rtState) {
	root := runtimeAddrRoot(expr)
	if root == nil || root.Name == "_" {
		return
	}
	if s.escaped == nil {
		s.escaped = map[*ast.Object]token.Pos{}
	}
	if root.Obj != nil {
		if _, isPkg := e.an.pkgVars[root.Obj]; !isPkg {
			s.escaped[root.Obj] = root.Pos()
		}
	}
}

// runtimeAddrRoot 取址表达式的根标识符：`n` / `(n)` / `obj.Field` / `slice[0]`。
func runtimeAddrRoot(expr ast.Expr) *ast.Ident {
	switch v := runtimeUnparen(expr).(type) {
	case *ast.Ident:
		return v
	case *ast.SelectorExpr:
		return runtimeAddrRoot(v.X)
	case *ast.IndexExpr:
		return runtimeAddrRoot(v.X)
	case *ast.StarExpr:
		return runtimeAddrRoot(v.X)
	}
	return nil
}
