package action

// todo24 §11.2/T04：表达式来源解析（rhsTypes/typeSources；从
// action_seq_dataflow.go 拆出——行数治理）。

import (
	"go/ast"
)

func rhsTypes(a *Actions, req CodeSequenceRequest, f *ast.File, imports map[string]string, expr ast.Expr, depth int, curPkg string) []writeSource {
	switch e := expr.(type) {
	case *ast.UnaryExpr: // &X
		return rhsTypes(a, req, f, imports, e.X, depth, curPkg)
	case *ast.ParenExpr:
		return rhsTypes(a, req, f, imports, e.X, depth, curPkg)
	case *ast.CompositeLit:
		return typeSources(e.Type, imports, curPkg)
	case *ast.CallExpr:
		// T04：builtin new(T) 的来源就是 T（未被同名函数/变量遮蔽时）。
		if id, ok := e.Fun.(*ast.Ident); ok && id.Name == "new" && id.Obj == nil && len(e.Args) == 1 {
			return typeSources(e.Args[0], imports, curPkg)
		}
		// T04：类型转换（`API(&Two{})` / `(*T)(x)`）——结果的具体来源是实参
		// 的值来源，不是被调"类型名"（否则会按类型名猜实现）。仅在 Fun 是
		// **类型对象**时成立（未被同名函数/变量遮蔽）。
		if len(e.Args) == 1 && seqExprIsTypeConversion(e.Fun) {
			return rhsTypes(a, req, f, imports, e.Args[0], depth, curPkg)
		}
		return callReturnTypes(a, req, f, imports, e.Fun, depth, curPkg)
	case *ast.Ident:
		if depth <= 0 {
			return nil
		}
		return varChainTypes(a, req, f, imports, e.Name, e.Obj, depth-1, curPkg)
	}
	return nil
}

// typeSources 类型表达式 → 候选（Ident 同包；SelectorExpr 跨包——
// import 映射）。
func typeSources(t ast.Expr, imports map[string]string, curPkg string) []writeSource {
	switch e := t.(type) {
	case *ast.Ident:
		return []writeSource{{typeName: e.Name, pkgPath: curPkg}}
	case *ast.SelectorExpr:
		if id, ok := e.X.(*ast.Ident); ok {
			if pkg, ok := imports[id.Name]; ok {
				return []writeSource{{typeName: e.Sel.Name, pkgPath: pkg}}
			}
		}
	}
	return nil
}

// seqExprIsTypeConversion CallExpr.Fun 是否是**类型**表达式（T(x) 转换）：
// 标识符对象是类型（ast.Typ）或 `*T` 这样的指针类型表达式。
func seqExprIsTypeConversion(fun ast.Expr) bool {
	switch x := fun.(type) {
	case *ast.Ident:
		return x.Obj != nil && x.Obj.Kind == ast.Typ
	case *ast.ParenExpr:
		return seqExprIsTypeConversion(x.X)
	case *ast.StarExpr:
		return true
	}
	return false
}
