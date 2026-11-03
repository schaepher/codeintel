package action

// U04t/U04u：导入包变量 receiver（`state.Shared.Work()`）的准确声明与类型。
//
// 只读查询主仓 go list 选中的外部源码；显式类型优先，其次按初始化表达式
// 恢复静态类型（字面量/取址/别名/包级单结果函数）。接口变量不选实现。

import (
	"go/ast"
	"go/token"
)

// seqExternalPkgVarType 解析 `<importPath>.<varName>` 的声明类型。
// 返回 (typeRef, interfaceType, ok)。
func seqExternalPkgVarType(a *Actions, req CodeSequenceRequest, importPath, varName string,
	depth int) (seqORMTypeRef, bool, bool) {
	if a == nil || importPath == "" || varName == "" || depth > 8 {
		return seqORMTypeRef{}, false, false
	}
	pkg := seqExternalPackage(a, req, importPath)
	if pkg == nil || pkg.err != nil {
		return seqORMTypeRef{}, false, false
	}
	var specs []*ast.ValueSpec
	var ctxs []*seqFileCtx
	for _, ctx := range pkg.files {
		if ctx == nil || ctx.file == nil || seqFileHasBuildConstraint(ctx) {
			continue
		}
		for _, d := range ctx.file.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, n := range vs.Names {
					if n.Name == varName {
						specs = append(specs, vs)
						ctxs = append(ctxs, ctx)
					}
				}
			}
		}
	}
	if len(specs) != 1 {
		return seqORMTypeRef{}, false, false // 缺失/重复声明都不证明
	}
	return seqExternalVarSpecType(a, req, pkg, ctxs[0], specs[0], importPath, depth)
}

// seqExternalVarSpecType 变量声明 → 静态类型（显式/推断）。
func seqExternalVarSpecType(a *Actions, req CodeSequenceRequest, pkg *seqExternalPkg,
	ctx *seqFileCtx, vs *ast.ValueSpec, importPath string, depth int) (seqORMTypeRef, bool, bool) {
	c := &seqPersistCtx{file: ctx.file, pkgPath: importPath, src: ctx.src, fset: ctx.fset,
		actions: a, repoAbs: req.RepoAbs, fileRel: ctx.rel, req: &req, ormModels: req.ormModels}
	if vs.Type != nil {
		if ref, ok := seqORMTypeExprRef(c, vs.Type, ctx.file, importPath, 0); ok {
			if seqExternalPkgTypeIsIface(pkg.files, ref.name, 0) {
				return ref, true, true
			}
			return ref, false, true
		}
		return seqORMTypeRef{}, false, false
	}
	if len(vs.Names) != len(vs.Values) || len(vs.Values) == 0 {
		return seqORMTypeRef{}, false, false
	}
	init := vs.Values[0]
	return seqExternalVarInitType(a, req, pkg, ctx, init, importPath, depth)
}

// seqExternalVarInitType 初始化表达式的静态类型（U04u）。
func seqExternalVarInitType(a *Actions, req CodeSequenceRequest, pkg *seqExternalPkg,
	ctx *seqFileCtx, e ast.Expr, importPath string, depth int) (seqORMTypeRef, bool, bool) {
	if e == nil || depth > 8 {
		return seqORMTypeRef{}, false, false
	}
	c := &seqPersistCtx{file: ctx.file, pkgPath: importPath, src: ctx.src, fset: ctx.fset,
		actions: a, repoAbs: req.RepoAbs, fileRel: ctx.rel, req: &req, ormModels: req.ormModels}
	switch v := runtimeUnparen(e).(type) {
	case *ast.UnaryExpr:
		if v.Op == token.AND {
			return seqExternalVarInitType(a, req, pkg, ctx, v.X, importPath, depth+1)
		}
		return seqORMTypeRef{}, false, false
	case *ast.CompositeLit:
		if ref, ok := seqORMTypeExprRef(c, v.Type, ctx.file, importPath, 0); ok {
			return ref, seqExternalPkgTypeIsIface(pkg.files, ref.name, 0), true
		}
	case *ast.Ident:
		// 同包变量别名（`var Alias = Inferred`）。
		if ref, iface, ok := seqExternalPkgVarType(a, req, importPath, v.Name, depth+1); ok {
			return ref, iface, true
		}
	case *ast.CallExpr:
		// 包级单结果函数（`func NewStore() *Store`）：只读签名；支持
		// `aliased.NewStore()`（按声明文件的 import 路径定位函数所在包）。
		fnPkgPath := importPath
		fnPkg := pkg
		if sel, ok := v.Fun.(*ast.SelectorExpr); ok {
			if base, ok2 := sel.X.(*ast.Ident); ok2 {
				p := seqImportPath(ctx.file, base.Name)
				if p == "" {
					return seqORMTypeRef{}, false, false
				}
				fnPkgPath = p
				fnPkg = seqExternalPackage(a, req, p)
			}
		}
		fn, fctx, ok := seqExternalPkgFunc(a, req, fnPkg, callFuncName(v))
		if !ok || fn == nil || fctx == nil || fn.Type.Results == nil ||
			len(fn.Type.Results.List) != 1 || len(fn.Type.Results.List[0].Names) > 1 {
			return seqORMTypeRef{}, false, false
		}
		fc := &seqPersistCtx{file: fctx.file, pkgPath: fnPkgPath, src: fctx.src, fset: fctx.fset,
			actions: a, repoAbs: req.RepoAbs, fileRel: fctx.rel, req: &req, ormModels: req.ormModels}
		if ref, ok := seqORMTypeExprRef(fc, fn.Type.Results.List[0].Type, fctx.file, fnPkgPath, 0); ok {
			files := pkg.files
			if fnPkg != nil {
				files = fnPkg.files
			}
			return ref, seqExternalPkgTypeIsIface(files, ref.name, 0), true
		}
	}
	return seqORMTypeRef{}, false, false
}

// seqExternalPkgFunc 外部包目录里的顶层函数声明。
func seqExternalPkgFunc(a *Actions, req CodeSequenceRequest, pkg *seqExternalPkg,
	name string) (*ast.FuncDecl, *seqFileCtx, bool) {
	if pkg == nil || name == "" {
		return nil, nil, false
	}
	for _, ctx := range pkg.files {
		if ctx == nil || ctx.file == nil || seqFileHasBuildConstraint(ctx) {
			continue
		}
		for _, d := range ctx.file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if ok && fd.Recv == nil && fd.Name != nil && fd.Name.Name == name {
				return fd, ctx, true
			}
		}
	}
	return nil, nil, false
}

// callFuncName 调用目标名（裸标识符或 pkg.Func 的最后一段）。
func callFuncName(call *ast.CallExpr) string {
	switch f := call.Fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}

// seqPkgVarReceiver 调用的接收者是否是 `pkg.Var`（导入包变量）。
func seqPkgVarReceiver(ctx *seqFileCtx, sel *ast.SelectorExpr) (string, string, bool) {
	if ctx == nil || ctx.file == nil || sel == nil {
		return "", "", false
	}
	inner, ok := runtimeUnparen(sel.X).(*ast.SelectorExpr)
	if !ok {
		return "", "", false
	}
	base, ok := inner.X.(*ast.Ident)
	if !ok || base.Obj != nil {
		return "", "", false // 局部变量遮蔽 import 别名
	}
	importPath := seqImportPath(ctx.file, base.Name)
	if importPath == "" {
		return "", "", false
	}
	return importPath, inner.Sel.Name, true
}
