package action

// todo23 §2.4：命名函数类型（TypeSpec/类型别名/跨包导入）识别——用于
// 空函数列表判定与"声明为函数类型"的逃逸判定。必须能处理遮蔽、类型别名
// 环与源码缺失；解析不到就按非函数类型处理（不猜）。

import (
	"go/ast"
	"strings"
)

// seqIsFuncTypeIn 类型表达式是否函数类型（含命名类型/别名/跨包导入）。
func seqIsFuncTypeIn(a *Actions, req CodeSequenceRequest, ctx *seqFileCtx, t ast.Expr, depth int) bool {
	return seqIsFuncTypeSeen(a, req, ctx, t, depth, map[string]bool{})
}

func seqIsFuncTypeSeen(a *Actions, req CodeSequenceRequest, ctx *seqFileCtx, t ast.Expr, depth int, seen map[string]bool) bool {
	if t == nil || depth > 4 {
		return false
	}
	switch x := t.(type) {
	case *ast.FuncType:
		return true
	case *ast.ParenExpr:
		return seqIsFuncTypeSeen(a, req, ctx, x.X, depth, seen)
	case *ast.Ident:
		if x.Obj != nil {
			if ts, ok := x.Obj.Decl.(*ast.TypeSpec); ok {
				return seqTypeSpecIsFunc(a, req, ctx, ts, pkgPathOf(req.curSym), depth, seen)
			}
			return false // 局部变量/形参：类型由其 ValueSpec/Field 决定
		}
		if ctx == nil {
			return false
		}
		return seqPkgTypeIsFunc(a, req, ctx, pkgPathOf(req.curSym), x.Name, depth, seen)
	case *ast.SelectorExpr:
		id, ok := x.X.(*ast.Ident)
		if !ok || ctx == nil || ctx.file == nil {
			return false
		}
		importPath := seqImportPath(ctx.file, id.Name)
		if importPath == "" {
			return false
		}
		pkg := seqExternalPackage(a, req, importPath)
		if pkg == nil || pkg.err != nil || pkg.std {
			return false
		}
		for _, fctx := range pkg.files {
			for _, decl := range fctx.file.Decls {
				gd, ok := decl.(*ast.GenDecl)
				if !ok || gd.Tok.String() != "type" {
					continue
				}
				for _, sp := range gd.Specs {
					ts, ok := sp.(*ast.TypeSpec)
					if !ok || ts.Name == nil || ts.Name.Name != x.Sel.Name {
						continue
					}
					return seqTypeSpecIsFunc(a, req, fctx, ts, importPath, depth+1, seen)
				}
			}
		}
	case *ast.StarExpr:
		// 指针类型不是函数类型（`*Check` 不能直接调用）
		return false
	}
	return false
}

// seqTypeSpecIsFunc TypeSpec 是否为函数类型（递归别名；环/超深 → false）。
func seqTypeSpecIsFunc(a *Actions, req CodeSequenceRequest, ctx *seqFileCtx,
	ts *ast.TypeSpec, pkgPath string, depth int, seen map[string]bool) bool {
	if ts == nil || ts.Name == nil {
		return false
	}
	key := pkgPath + ":" + ts.Name.Name
	if seen[key] {
		return false // 循环类型：按非函数类型处理（不猜）
	}
	seen[key] = true
	defer delete(seen, key)
	if _, ok := ts.Type.(*ast.FuncType); ok {
		return true
	}
	sub := req
	sub.curSym = "symbol:go:" + pkgPath + ":" + ts.Name.Name
	return seqIsFuncTypeSeen(a, sub, ctx, ts.Type, depth+1, seen)
}

// seqPkgTypeIsFunc 同包（当前目录/仓库文件）里按名字找 TypeSpec（同文件
// 无 Obj 的兄弟文件声明）。
func seqPkgTypeIsFunc(a *Actions, req CodeSequenceRequest, ctx *seqFileCtx,
	pkgPath, name string, depth int, seen map[string]bool) bool {
	key := pkgPath + ":" + name
	if seen[key] {
		return false
	}
	seen[key] = true
	defer delete(seen, key)
	if ctx.file != nil {
		for _, decl := range ctx.file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok.String() != "type" {
				continue
			}
			for _, sp := range gd.Specs {
				ts, ok := sp.(*ast.TypeSpec)
				if ok && ts.Name != nil && ts.Name.Name == name {
					return seqTypeSpecIsFunc(a, req, ctx, ts, pkgPath, depth+1, seen)
				}
			}
		}
	}
	// 仓库内同包兄弟文件（外部包同包兄弟文件不在本文件的 import 图里，
	// 保持非函数类型——不猜）。
	if strings.HasPrefix(ctx.rel, "<external>/") {
		return false
	}
	for _, rel := range seqPackageFiles(req) {
		if rel == ctx.rel {
			continue
		}
		fctx := seqFileCtxFor(req, rel)
		if fctx == nil || fctx.file == nil {
			continue
		}
		for _, decl := range fctx.file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok.String() != "type" {
				continue
			}
			for _, sp := range gd.Specs {
				ts, ok := sp.(*ast.TypeSpec)
				if ok && ts.Name != nil && ts.Name.Name == name {
					return seqTypeSpecIsFunc(a, req, fctx, ts, pkgPath, depth+1, seen)
				}
			}
		}
	}
	return false
}

// seqExprFuncTypedIn 表达式是否声明为函数类型（含命名类型/别名/跨包）。
func seqExprFuncTypedIn(a *Actions, req CodeSequenceRequest, ctx *seqFileCtx, expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.FuncLit:
		return true
	case *ast.Ident:
		if e.Obj == nil {
			return false
		}
		switch d := e.Obj.Decl.(type) {
		case *ast.Field:
			return seqIsFuncTypeIn(a, req, ctx, d.Type, 0)
		case *ast.ValueSpec:
			if d.Type == nil {
				return false
			}
			return seqIsFuncTypeIn(a, req, ctx, d.Type, 0)
		}
	}
	return false
}

// seqEmptyFuncSliceTypeIn 声明类型是否为 []func(...) / []NamedFunc（nil slice
// = 确定空列表）。
func seqEmptyFuncSliceTypeIn(a *Actions, req CodeSequenceRequest, ctx *seqFileCtx, t ast.Expr) bool {
	at, ok := t.(*ast.ArrayType)
	if !ok || at.Elt == nil {
		return false
	}
	return seqIsFuncTypeIn(a, req, ctx, at.Elt, 0)
}
