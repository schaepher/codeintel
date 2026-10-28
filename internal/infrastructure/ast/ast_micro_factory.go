package ast

// T09：micro 注册实参的工厂返回类型证明。
//
// `c.Get().(pb.Handler)` 的静态类型是接口/any；从工厂函数体**全部第一返回槽**
// 的唯一具体具名类型证明实现（nil/接口/不一致 → 不猜）。

import (
	"go/ast"
	"strings"

	"go/types"
	"golang.org/x/tools/go/packages"
)

// isIfaceOrAny 类型是否为接口（含 interface{}）。
func isIfaceOrAny(t types.Type) bool {
	if t == nil {
		return true
	}
	_, ok := t.Underlying().(*types.Interface)
	return ok
}

// microFactoryReturnType 工厂调用 expr 的第一个返回槽在**全部 return 路径**上
// 的唯一具体具名类型（指针自动解引用）。任一返回是 nil/接口/字面量或类型
// 不一致 → 失败（不猜）。
func microFactoryReturnType(pkg *packages.Package, expr ast.Expr) (types.Type, bool) {
	call, ok := unwrapMicroCall(expr)
	if !ok {
		return nil, false
	}
	obj := microCallObject(pkg, call)
	fn, ok := obj.(*types.Func)
	if !ok {
		return nil, false
	}
	fd := microFuncDecl(pkg, fn)
	if fd == nil || fd.Body == nil {
		return nil, false
	}
	var got types.Type
	found := false
	bad := false
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		ret, ok := n.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		if len(ret.Results) == 0 {
			bad = true
			return true
		}
		t := pkg.TypesInfo.TypeOf(ret.Results[0])
		named, ok2 := concreteNamedType(t)
		if !ok2 {
			bad = true
			return true
		}
		if got == nil {
			got = named
			found = true
			return true
		}
		if !types.Identical(got, named) {
			bad = true
		}
		return true
	})
	if bad || !found || got == nil {
		return nil, false
	}
	return got, true
}

func unwrapMicroCall(expr ast.Expr) (*ast.CallExpr, bool) {
	for {
		switch e := expr.(type) {
		case *ast.ParenExpr:
			expr = e.X
			continue
		case *ast.CallExpr:
			return e, true
		}
		return nil, false
	}
}

// microCallObject 调用表达式被调对象的类型对象（函数/方法）。
func microCallObject(pkg *packages.Package, call *ast.CallExpr) types.Object {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		if o := pkg.TypesInfo.Uses[fn]; o != nil {
			return o
		}
		return pkg.TypesInfo.Defs[fn]
	case *ast.SelectorExpr:
		if sel, ok := pkg.TypesInfo.Selections[fn]; ok && sel != nil {
			return sel.Obj()
		}
		if o := pkg.TypesInfo.Uses[fn.Sel]; o != nil {
			return o
		}
	}
	return nil
}

// microFuncDecl 类型对象 → 本包函数声明（Defs 身份匹配；跨包返回 nil）。
func microFuncDecl(pkg *packages.Package, fn *types.Func) *ast.FuncDecl {
	for _, f := range pkg.Syntax {
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Name == nil {
				continue
			}
			if obj := pkg.TypesInfo.Defs[fd.Name]; obj != nil && obj == fn {
				return fd
			}
		}
	}
	return nil
}

// concreteNamedType 具体具名类型（排除接口/指针解引用）；其它返回 false。
func concreteNamedType(t types.Type) (types.Type, bool) {
	if t == nil {
		return nil, false
	}
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok {
		return nil, false
	}
	if _, isIface := named.Underlying().(*types.Interface); isIface {
		return nil, false
	}
	if strings.HasPrefix(named.Obj().Name(), "Unimplemented") {
		return nil, false
	}
	return named, true
}
