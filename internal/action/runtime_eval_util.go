package action

// todo25 §13：表达式求值的通用小工具（从 runtime_eval_expr.go 拆出）。
import (
	"go/ast"
	"strings"
)

func runtimeCallName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}

func runtimeUnparen(e ast.Expr) ast.Expr {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			return e
		}
		e = p.X
	}
}

func runtimeIsNilLiteral(e ast.Expr) bool {
	id, ok := runtimeUnparen(e).(*ast.Ident)
	return ok && id.Name == "nil" && id.Obj == nil
}

func runtimeStringsJoin(vals []rtValue, sep string) string {
	parts := make([]string, 0, len(vals))
	for _, v := range vals {
		parts = append(parts, v.rtDisplay())
	}
	return strings.Join(parts, sep)
}

func isRuntimeStructType(t ast.Expr) bool {
	switch x := t.(type) {
	case *ast.StructType:
		return true
	case *ast.Ident:
		return x.Obj != nil && x.Obj.Kind == ast.Typ
	case *ast.SelectorExpr:
		return true
	case *ast.StarExpr:
		return isRuntimeStructType(x.X)
	}
	return false
}
