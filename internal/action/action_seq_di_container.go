package action

import "go/ast"

// diPickRegistration 按注解 key（或字段名）选注册：多重注册 → 未消歧。
func diPickRegistration(regs map[string][]seqDIReg, key, field string) (seqDIPick, bool) {
	for _, k := range []string{key, field} {
		if k == "" {
			continue
		}
		if list, ok := regs[k]; ok {
			switch len(list) {
			case 0:
				continue
			case 1:
				return seqDIPick{reg: list[0], key: k}, true
			default:
				return seqDIPick{key: k, multi: true}, true
			}
		}
	}
	return seqDIPick{}, false
}

// isReflectTypeOfArg 注册实参是否为 reflect.TypeOf(...)（T06 反射容器形态）。
func isReflectTypeOfArg(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "TypeOf" {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == "reflect"
}

// seqContainerName 容器表达式的名字（`di.Container` / `Container` → Container）。
func seqContainerName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return x.Sel.Name
	case *ast.ParenExpr:
		return seqContainerName(x.X)
	}
	return ""
}
