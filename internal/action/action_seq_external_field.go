package action

// U04r/U04s：外部结构体字段声明的准确解析（声明文件自身的 import；显式
// 匿名嵌入字段）。
//
// 字段读的是**声明**：不是实例来源、非 nil 证明、执行成功或事务提交证明。

import (
	"go/ast"
)

// seqPkgFieldTypeRef 解析包内结构体字段的静态类型（跨文件、用声明文件 import）。
// 返回 (typeRef, interfaceField, ok)。
func seqPkgFieldTypeRef(a *Actions, req CodeSequenceRequest, pkgPath, relFile, typeName, field string,
	depth int) (seqORMTypeRef, bool, bool) {
	if a == nil || typeName == "" || field == "" || depth > 8 {
		return seqORMTypeRef{}, false, false
	}
	files := seqPkgFilesForRef(a, req, pkgPath, relFile)
	for _, ctx := range files {
		if ctx == nil || ctx.file == nil || seqFileHasBuildConstraint(ctx) {
			continue
		}
		ts := fileTypeSpec(ctx.file, typeName)
		if ts == nil {
			continue
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			continue
		}
		ref, iface, ok := seqStructFieldInDecl(a, req, pkgPath, ctx, st, field, depth)
		if ok {
			return ref, iface, true
		}
	}
	return seqORMTypeRef{}, false, false
}

// seqStructFieldInDecl 结构体声明里的字段类型（含显式匿名嵌入字段）。
func seqStructFieldInDecl(a *Actions, req CodeSequenceRequest, pkgPath string, ctx *seqFileCtx,
	st *ast.StructType, field string, depth int) (seqORMTypeRef, bool, bool) {
	c := &seqPersistCtx{file: ctx.file, pkgPath: pkgPath, src: ctx.src, fset: ctx.fset,
		actions: a, repoAbs: req.RepoAbs, fileRel: ctx.rel, req: &req, ormModels: req.ormModels}
	for _, fl := range st.Fields.List {
		if len(fl.Names) == 0 {
			// 显式匿名字段访问（`tx.Tx`）：字段名 = 类型名（指针去掉）。
			ft := fl.Type
			if star, ok := ft.(*ast.StarExpr); ok {
				ft = star.X
			}
			if exprTypeName(ft) != field {
				continue
			}
			return seqStructFieldRef(c, a, req, pkgPath, fl.Type, depth)
		}
		for _, n := range fl.Names {
			if n.Name != field {
				continue
			}
			return seqStructFieldRef(c, a, req, pkgPath, fl.Type, depth)
		}
	}
	return seqORMTypeRef{}, false, false
}

// seqStructFieldRef 字段类型表达式 → 类型引用（接口字段单独标记）。
func seqStructFieldRef(c *seqPersistCtx, a *Actions, req CodeSequenceRequest, pkgPath string,
	typ ast.Expr, depth int) (seqORMTypeRef, bool, bool) {
	ref, ok := seqORMTypeExprRef(c, typ, c.file, pkgPath, 0)
	if !ok {
		return seqORMTypeRef{}, false, false
	}
	if seqIsExternalIfaceRef(a, req, ref) {
		return ref, true, true
	}
	return ref, false, true
}

// seqIsExternalIfaceRef 类型引用是否为接口（在本包或外部声明里）。
func seqIsExternalIfaceRef(a *Actions, req CodeSequenceRequest, ref seqORMTypeRef) bool {
	if ref.pkgPath == "" || ref.name == "" {
		return false
	}
	pkg := seqExternalPackage(a, req, ref.pkgPath)
	if pkg == nil || pkg.err != nil {
		return false
	}
	return seqExternalPkgTypeIsIface(pkg.files, ref.name, 0)
}

// seqPkgFilesForRef 类型所在包的文件（仓库内/外部）。
func seqPkgFilesForRef(a *Actions, req CodeSequenceRequest, pkgPath, relFile string) []*seqFileCtx {
	if a == nil || req.ctx().Err() != nil {
		return nil
	}
	pkg := seqExternalPackage(a, req, pkgPath)
	if pkg != nil && pkg.err == nil && len(pkg.files) > 0 {
		return pkg.files
	}
	// 仓库内包：按同目录文件枚举。
	var out []*seqFileCtx
	for _, rel := range seqPackageFilesOf(req, relFile) {
		if ctx := seqFileCtxFor(req, rel); ctx != nil {
			out = append(out, ctx)
		}
	}
	return out
}
