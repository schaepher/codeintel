package action

// todo25 §13：轻量类型/变参展开（从 runtime_eval_call.go 拆出）。

import "go/ast"

// runtimeTypeOfTypeExpr 类型表达式 → 轻量类型名（同包；跨包首期只用短名）。
func runtimeTypeOfTypeExpr(_ *ast.Object, e ast.Expr) *runtimeType {
	name := runtimeTypeNameOfExpr(e)
	if name == "" {
		return nil
	}
	return &runtimeType{name: name}
}

func runtimeTypeNameOfExpr(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.StarExpr:
		return runtimeTypeNameOfExpr(x.X)
	case *ast.SelectorExpr:
		return x.Sel.Name
	case *ast.IndexExpr:
		return runtimeTypeNameOfExpr(x.X)
	case *ast.IndexListExpr:
		return runtimeTypeNameOfExpr(x.X)
	}
	return ""
}

func bindRuntimeParamTypes(types map[*ast.Object]*runtimeType, params *ast.FieldList) {
	if params == nil {
		return
	}
	for _, fl := range params.List {
		name := runtimeTypeNameOfExpr(fl.Type)
		if name == "" {
			continue
		}
		for _, nm := range fl.Names {
			if nm.Obj != nil {
				types[nm.Obj] = &runtimeType{name: name}
			}
		}
	}
}

// runtimeTypeFromExpr 变量/调用的轻量类型（用于方法解析）。
func (e *rtEval) runtimeTypeFromExpr(frame *rtFrame, expr ast.Expr, s *rtState, depth int) *runtimeType {
	switch x := expr.(type) {
	case *ast.UnaryExpr:
		if x.Op.String() == "&" {
			return e.runtimeTypeFromExpr(frame, x.X, s, depth)
		}
	case *ast.ParenExpr:
		return e.runtimeTypeFromExpr(frame, x.X, s, depth)
	case *ast.CompositeLit:
		if name := runtimeTypeNameOfExpr(x.Type); name != "" {
			return &runtimeType{name: name}
		}
	case *ast.Ident:
		if t, ok := s.types[x.Obj]; ok {
			return t
		}
	case *ast.CallExpr:
		if target, ok := runtimeCalleeID(frame, x); ok {
			if fn, _, ok2 := seqFuncSource(e.a, e.req, target); ok2 && fn.Type.Results != nil && len(fn.Type.Results.List) > 0 {
				if name := runtimeTypeNameOfExpr(fn.Type.Results.List[0].Type); name != "" {
					return &runtimeType{name: name}
				}
			}
			// 方法调用：按 receiver 类型 + 方法名找声明
			if sel, ok2 := x.Fun.(*ast.SelectorExpr); ok2 {
				if recvType := e.runtimeTypeFromExpr(frame, sel.X, s, depth); recvType != nil {
					id := "symbol:go:" + frame.pkgPath + ":(" + recvType.name + ")." + sel.Sel.Name
					if fn, _, ok3 := seqFuncSource(e.a, e.req, id); ok3 && fn.Type.Results != nil && len(fn.Type.Results.List) > 0 {
						if name := runtimeTypeNameOfExpr(fn.Type.Results.List[0].Type); name != "" {
							return &runtimeType{name: name}
						}
					}
				}
			}
		}
	}
	return nil
}

func flattenRuntimeVariadic(vals []rtValue, call *ast.CallExpr) []rtValue {
	if call == nil || !call.Ellipsis.IsValid() || len(vals) == 0 {
		return vals
	}
	last := vals[len(vals)-1]
	if last.kind != rtSlice {
		return vals
	}
	out := append([]rtValue(nil), vals[:len(vals)-1]...)
	return append(out, last.elems...)
}
