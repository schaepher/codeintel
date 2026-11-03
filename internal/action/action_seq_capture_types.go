package action

import (
	"go/ast"
	"go/token"
)

// seqTypeOfExpr 表达式的接收者类型（构造器/字面量/变量链；纯语法数据流）。
func seqTypeOfExpr(a *Actions, req CodeSequenceRequest, ctx *seqFileCtx, expr ast.Expr) (seqTypeRef, bool) {
	switch e := expr.(type) {
	case *ast.UnaryExpr:
		if e.Op.String() == "&" {
			return seqTypeOfExpr(a, req, ctx, e.X)
		}
	case *ast.CompositeLit:
		return seqTypeRefOf(req, ctx, e.Type)
	case *ast.ParenExpr:
		return seqTypeOfExpr(a, req, ctx, e.X)
	case *ast.CallExpr:

		if isNilIdent(e.Args) {
			if ref, ok := seqTypeRefOf(req, ctx, unwrapParen(e.Fun)); ok {
				return ref, true
			}
		}

		if sel, ok := e.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "TypeOf" && len(e.Args) == 1 {
			if id, ok2 := sel.X.(*ast.Ident); ok2 && id.Obj == nil {
				if p := seqImportPath(ctx.file, id.Name); p == "reflect" {
					return seqTypeOfExpr(a, req, ctx, e.Args[0])
				}
			}
		}

		imports := importAliases(ctx.file)
		refs := rhsTypes(a, req, ctx.file, imports, e, 3, pkgPathOf(req.curSym))
		if len(refs) == 1 {
			return seqTypeRef{pkgPath: refs[0].pkgPath, typeName: refs[0].typeName}, true
		}

		if ref, ok := seqExternalFuncResultType(a, req, ctx, e); ok {
			return ref, true
		}
	case *ast.Ident:
		return seqLookupType(req.env, e.Obj)
	}
	return seqTypeRef{}, false
}

// unwrapParen 剥掉括号（`(*T)` → `*T`）。
func unwrapParen(e ast.Expr) ast.Expr {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			return e
		}
		e = p.X
	}
}

// seqTypeRefOf 类型表达式 → 类型引用（Ident / pkg.T / 泛型 T[P]）。
func seqTypeRefOf(req CodeSequenceRequest, ctx *seqFileCtx, t ast.Expr) (seqTypeRef, bool) {
	switch x := t.(type) {
	case *ast.Ident:
		return seqTypeRef{pkgPath: pkgPathOf(req.curSym), typeName: x.Name}, true
	case *ast.SelectorExpr:
		if id, ok := x.X.(*ast.Ident); ok {
			if p := seqImportPath(ctx.file, id.Name); p != "" {
				return seqTypeRef{pkgPath: p, typeName: x.Sel.Name}, true
			}
		}
	case *ast.StarExpr:
		return seqTypeRefOf(req, ctx, x.X)
	case *ast.Ellipsis:
		return seqTypeRefOf(req, ctx, x.Elt)
	case *ast.IndexExpr:
		return seqTypeRefOf(req, ctx, x.X)
	case *ast.IndexListExpr:
		return seqTypeRefOf(req, ctx, x.X)
	}
	return seqTypeRef{}, false
}

// seqLookupType 按声明对象查类型（沿闭包父环境）。
func seqLookupType(env *seqEnv, obj *ast.Object) (seqTypeRef, bool) {
	for e := env; e != nil; e = e.parent {
		if r, ok := e.types[obj]; ok {
			return r, true
		}
	}
	return seqTypeRef{}, false
}

// lineOfOffset 位置 → 行号（ctx 可能为 nil）。
func lineOfOffset(ctx *seqFileCtx, pos token.Pos) int {
	if ctx == nil || ctx.fset == nil || !pos.IsValid() {
		return 0
	}
	return ctx.fset.Position(pos).Line
}

// seqReceiverTypeRef 方法调用接收者的类型（`extT.Method()` / `h.store.Method()`）：
//  1. 局部变量：按声明对象查类型数据流（声明/复合字面量/构造器返回）；
//  2. 当前 receiver 的字段：按结构体声明取字段类型（`s.store.Method()`）；
//  3. 已知类型的变量字段：同文件结构体声明（不做跨文件猜测）。
func seqReceiverTypeRef(a *Actions, req CodeSequenceRequest, ctx *seqFileCtx,
	sel *ast.SelectorExpr) (seqTypeRef, bool) {
	if sel == nil {
		return seqTypeRef{}, false
	}
	switch x := sel.X.(type) {
	case *ast.Ident:
		if r, ok := seqLookupType(req.env, x.Obj); ok {
			return r, true
		}
	case *ast.SelectorExpr:
		base, ok := x.X.(*ast.Ident)
		if !ok {
			return seqTypeRef{}, false
		}
		rel := ""
		if ctx != nil {
			rel = ctx.rel
		}
		if ref, ok := seqLookupType(req.env, base.Obj); ok {
			// U04r：字段类型按**结构体声明所在包/文件**解析（方法与结构体
			// 可在不同文件，import 也不同）。
			if fr, _, ok2 := seqPkgFieldTypeRef(a, req, ref.pkgPath, rel, ref.typeName,
				x.Sel.Name, 0); ok2 {
				out := seqTypeRef{pkgPath: fr.pkgPath, typeName: fr.name}
				if out.pkgPath == pkgPathOf(req.curSym) {
					out.pkgPath = ref.pkgPath
				}
				return out, true
			}
		}
		if req.curRecvType != "" {
			// receiver 字段（含显式匿名嵌入字段）同样按包内声明解析。
			if fr, _, ok2 := seqPkgFieldTypeRef(a, req, pkgPathOf(req.curSym), rel,
				req.curRecvType, x.Sel.Name, 0); ok2 {
				return seqTypeRef{pkgPath: fr.pkgPath, typeName: fr.name}, true
			}
			if fr, ok2 := seqStructFieldTypeRef(req, ctx, req.curRecvType, x.Sel.Name); ok2 {
				return fr, true
			}
		}
	}
	return seqTypeRef{}, false
}

// seqStructFieldTypeRef 同文件结构体声明的字段类型（含 pkg.T / *T / 泛型）。
func seqStructFieldTypeRef(req CodeSequenceRequest, ctx *seqFileCtx, typeName, field string) (seqTypeRef, bool) {
	if ctx == nil || ctx.file == nil || typeName == "" || field == "" {
		return seqTypeRef{}, false
	}
	for _, decl := range ctx.file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, sp := range gd.Specs {
			tsp, ok := sp.(*ast.TypeSpec)
			if !ok || tsp.Name == nil || tsp.Name.Name != typeName {
				continue
			}
			st, ok := tsp.Type.(*ast.StructType)
			if !ok || st.Fields == nil {
				continue
			}
			for _, fl := range st.Fields.List {
				for _, nm := range fl.Names {
					if nm.Name == field {
						return seqTypeRefOf(req, ctx, fl.Type)
					}
				}
			}
		}
	}
	return seqTypeRef{}, false
}

// seqExternalFuncResultType 外部包包级函数/构造器的第一个返回类型。
func seqExternalFuncResultType(a *Actions, req CodeSequenceRequest, ctx *seqFileCtx, call *ast.CallExpr) (seqTypeRef, bool) {
	if ctx == nil || ctx.file == nil || call == nil {
		return seqTypeRef{}, false
	}
	importPath, declName, _, ok := seqExternalCallTarget(ctx.file, call.Fun)
	if !ok || req.pkgSet[importPath] {
		return seqTypeRef{}, false
	}
	pkg := seqExternalPackage(a, req, importPath)
	if pkg == nil || pkg.err != nil || pkg.std {
		return seqTypeRef{}, false
	}
	ectx, fd := seqFindExternalDecl(pkg, declName)
	if ectx == nil || fd == nil || fd.Type.Results == nil || len(fd.Type.Results.List) == 0 {
		return seqTypeRef{}, false
	}
	sub := req
	sub.curSym = "symbol:go:" + importPath + ":" + declName
	return seqTypeRefOf(sub, ectx, fd.Type.Results.List[0].Type)
}
