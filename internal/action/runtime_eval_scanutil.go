package action

// todo25 §13：Scan 目标类型兼容性（从 runtime_eval_sql.go 拆出）。

import "go/ast"

// runtimeScanTargetAccepts Scan 目标声明类型与行值兼容性（null/布尔/分数
// 不得当整数；目标类型未知时只按值保留）。
func runtimeScanTargetAccepts(target ast.Expr, v rtValue) bool {
	if v.kind == rtUnknown {
		return true
	}
	ident := runtimeScanIdent(target)
	if ident == nil || ident.Obj == nil {
		return true
	}
	typ := ""
	switch d := ident.Obj.Decl.(type) {
	case *ast.ValueSpec:
		typ = exprTypeName(d.Type)
	case *ast.Field:
		typ = exprTypeName(d.Type)
	}
	switch typ {
	case "", "any", "interface{}":
		return true
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
		return v.kind == rtInt
	case "float32", "float64":
		return v.kind == rtFloat || v.kind == rtInt
	case "string":
		return v.kind == rtString
	case "bool":
		return v.kind == rtBool
	}
	return true
}

func runtimeScanIdent(target ast.Expr) *ast.Ident {
	if u, ok := target.(*ast.UnaryExpr); ok {
		target = u.X
	}
	switch t := target.(type) {
	case *ast.Ident:
		return t
	case *ast.SelectorExpr:
		if id, ok := t.X.(*ast.Ident); ok {
			return id
		}
	}
	return nil
}
