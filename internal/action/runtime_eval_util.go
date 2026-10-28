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

func (e *rtEval) evalStringsBuiltin(frame *rtFrame, call *ast.CallExpr, name string, s *rtState, depth int) (rtValue, []*rtState, bool) {
	switch name {
	case "Repeat":
		if len(call.Args) != 2 {
			return rtValue{}, nil, false
		}
		sv, variants := e.evalExpr(frame, call.Args[0], s, depth)
		cv, _ := e.evalExpr(frame, call.Args[1], s, depth)
		if sv.kind == rtString && cv.kind == rtInt && cv.i >= 0 && cv.i < 1024 {
			return rtStringValue(strings.Repeat(sv.s, int(cv.i))), variants, true
		}
		return rtUnknownValue(), variants, true
	case "Join":
		if len(call.Args) != 2 {
			return rtValue{}, nil, false
		}
		sv, variants := e.evalExpr(frame, call.Args[0], s, depth)
		sep, _ := e.evalExpr(frame, call.Args[1], s, depth)
		if sv.kind == rtSlice && sep.kind == rtString {
			parts := make([]string, 0, len(sv.elems))
			for _, el := range sv.elems {
				if el.kind != rtString {
					return rtUnknownValue(), variants, true
				}
				parts = append(parts, el.s)
			}
			return rtStringValue(strings.Join(parts, sep.s)), variants, true
		}
		return rtUnknownValue(), variants, true
	}
	return rtValue{}, nil, false
}
