package ast

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/packages"
)

// funcValueRef 解析函数值引用（P2-1）：g（函数名 Ident）或 obj.M
// （方法值 SelectorExpr）；非函数值返回 ok=false。
func funcValueRef(pkg *packages.Package, expr ast.Expr) (*types.Func, bool) {
	switch e := expr.(type) {
	case *ast.Ident:
		if fn, ok := pkg.TypesInfo.ObjectOf(e).(*types.Func); ok {
			return fn, true
		}
	case *ast.SelectorExpr:
		if sel := pkg.TypesInfo.Selections[e]; sel != nil {
			if fn, ok := sel.Obj().(*types.Func); ok {
				return fn, true
			}
		}
	}
	return nil, false
}

// isFuncSetType 判断类型是否为"函数集合"：切片/数组/映射的元素是函数
// 类型，或 struct 字段里含函数类型（如 []WrapperValidator、map[string]func()）。
// seen 访问集防止自引用类型（struct A{ *A; F func() }）递归爆栈。
func isFuncSetType(t types.Type) bool {
	return funcSetType(t, map[types.Type]bool{})
}

// funcSetType 递归判定函数集合类型（seen 为访问集）。
func funcSetType(t types.Type, seen map[types.Type]bool) bool {
	if t == nil || seen[t] {
		return false
	}
	seen[t] = true
	switch tt := t.(type) {
	case *types.Named:
		return funcSetType(tt.Underlying(), seen)
	case *types.Pointer:
		return funcSetType(tt.Elem(), seen)
	case *types.Slice:
		return funcSetType(tt.Elem(), seen)
	case *types.Array:
		return funcSetType(tt.Elem(), seen)
	case *types.Map:
		return funcSetType(tt.Elem(), seen) || funcSetType(tt.Key(), seen)
	case *types.Signature:
		return true
	case *types.Struct:
		for i := 0; i < tt.NumFields(); i++ {
			if funcSetType(tt.Field(i).Type(), seen) {
				return true
			}
		}
	}
	return false
}

// tupleHasFuncSet 元组里是否有函数集合类型参数/返回值。
func tupleHasFuncSet(t *types.Tuple) bool {
	if t == nil {
		return false
	}
	for i := 0; i < t.Len(); i++ {
		if isFuncSetType(t.At(i).Type()) {
			return true
		}
	}
	return false
}

// collectFuncSetRefs 从表达式收集"函数集合"里注册的函数值引用（复合
// 字面量的元素/字段——嵌套结构递归；&T{} 解包）。seen 按类型去重，
// 防自引用类型图递归。只收集具名函数/方法值（argFuncRef 能解析的）。
func collectFuncSetRefs(pkg *packages.Package, expr ast.Expr) []*types.Func {
	var out []*types.Func
	seen := map[types.Type]bool{}
	var walk func(ast.Expr)
	walk = func(e ast.Expr) {
		switch x := e.(type) {
		case *ast.UnaryExpr:
			walk(x.X)
		case *ast.CompositeLit:
			t := pkg.TypesInfo.TypeOf(x)
			if t != nil {
				if seen[t] {
					return
				}
				seen[t] = true
			}
			for _, el := range x.Elts {
				if kv, ok := el.(*ast.KeyValueExpr); ok {
					walk(kv.Value)
				} else {
					walk(el)
				}
			}
		default:
			if fn := argFuncRef(pkg, e); fn != nil {
				out = append(out, fn)
			}
		}
	}
	walk(expr)
	return out
}

// argFuncRef 将调用参数解析为函数引用（作为参数传入的回调）：
//
//	Ident（home）                          → 具名函数
//	SelectorExpr（s.PageHome / pkg.F）     → 方法/包函数引用
//	CallExpr（http.HandlerFunc(home)）     → 解包 HandlerFunc 包装
//
// 非函数引用（变量/字面量/匿名函数）返回 nil。
func argFuncRef(pkg *packages.Package, arg ast.Expr) *types.Func {
	var obj types.Object
	switch a := arg.(type) {
	case *ast.Ident:
		obj = pkg.TypesInfo.Uses[a]
	case *ast.SelectorExpr:
		obj = pkg.TypesInfo.Uses[a.Sel]
	case *ast.CallExpr:

		if sel, ok := a.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "HandlerFunc" && len(a.Args) == 1 {
			if id, ok := a.Args[0].(*ast.Ident); ok {
				obj = pkg.TypesInfo.Uses[id]
			}
		}
	}
	if obj == nil {
		return nil
	}
	fn, _ := obj.(*types.Func)
	return fn
}
