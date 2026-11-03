package action

// U04g：类型声明/结构体字段解析（别名跟随、局部类型拒绝、字段原文）。

import (
	"go/ast"
	"go/token"
	"strings"
)

// seqFileLevelTypeSpec 文件级 type 声明（局部类型不算）。
func seqFileLevelTypeSpec(f *ast.File, name string) bool {
	if f == nil || name == "" {
		return false
	}
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name != nil && ts.Name.Name == name {
				return true
			}
		}
	}
	return false
}

// resolveFuncDecl 调用目标 → 包级函数声明（不含方法；同包跨文件也支持）。
func (c *seqPersistCtx) resolveFuncDecl(fun ast.Expr) (*ast.FuncDecl, *ast.File, string, bool) {
	switch f := fun.(type) {
	case *ast.Ident:
		if f.Obj != nil {
			fd, ok := f.Obj.Decl.(*ast.FuncDecl)
			if !ok || fd.Recv != nil {
				return nil, nil, "", false
			}
			return fd, c.file, c.pkgPath, true
		}
		fd, file, ok := c.pkgFuncDeclByName(f.Name)
		if !ok {
			return nil, nil, "", false
		}
		return fd, file, c.pkgPath, true
	case *ast.SelectorExpr:
		id, ok := f.X.(*ast.Ident)
		if !ok {
			return nil, nil, "", false
		}
		pkg := seqImportPath(c.file, id.Name)
		if pkg == "" {
			return nil, nil, "", false
		}
		return c.repoFuncDecl(pkg, f.Sel.Name)
	}
	return nil, nil, "", false
}

// pkgFuncDeclByName 同包顶层函数声明（跨文件）。
func (c *seqPersistCtx) pkgFuncDeclByName(name string) (*ast.FuncDecl, *ast.File, bool) {
	for _, rel := range seqPackageFilesOf(CodeSequenceRequest{RepoAbs: c.repoAbs}, c.fileRel) {
		ctx := c.fileCtxFor(rel)
		if ctx == nil || ctx.file == nil {
			continue
		}
		for _, d := range ctx.file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if ok && fd.Recv == nil && fd.Name != nil && fd.Name.Name == name {
				return fd, ctx.file, true
			}
		}
	}
	return nil, nil, false
}

// repoFuncDecl 外部包函数声明（索引定位 + 源码解析）。
func (c *seqPersistCtx) repoFuncDecl(pkgPath, name string) (*ast.FuncDecl, *ast.File, string, bool) {
	if c.actions == nil || c.repoAbs == "" || pkgPath == "" || name == "" {
		return nil, nil, "", false
	}
	ctx := c.externalFileCtx("symbol:go:" + pkgPath + ":" + name)
	if ctx == nil || ctx.file == nil {
		return nil, nil, "", false
	}
	for _, d := range ctx.file.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if ok && fd.Recv == nil && fd.Name != nil && fd.Name.Name == name {
			return fd, ctx.file, pkgPath, true
		}
	}
	return nil, nil, "", false
}

// resolveORMModel 类型引用 → 结构体声明 + 字段（别名跟随，缓存按 symbol ID）。
func (c *seqPersistCtx) resolveORMModel(ref seqORMTypeRef) *seqORMStruct {
	seen := map[string]bool{}
	cur := ref
	for i := 0; i < seqORMModelMaxDepth; i++ {
		id := "symbol:go:" + cur.pkgPath + ":" + cur.name
		if seen[id] {
			return nil // 别名环
		}
		seen[id] = true
		if c.ormModels != nil {
			if m, ok := c.ormModels[id]; ok {
				return m
			}
		}
		ts, ctx := c.typeSpecOf(cur)
		if ts == nil || ts.TypeParams != nil || ts.Name == nil {
			return nil
		}
		if ts.Assign.IsValid() {
			// 真正的别名：跟随右侧类型（`type Defined Request` 不是别名）。
			next, ok := seqORMTypeExprRef(c, ts.Type, ctx.file, cur.pkgPath, 0)
			if !ok {
				return nil
			}
			cur = next
			continue
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return nil
		}
		model := &seqORMStruct{SymbolID: id, Package: cur.pkgPath, File: ctx.rel,
			Fields: seqORMStructFields(ctx.fset, ctx.src, st)}
		if c.ormModels != nil {
			c.ormModels[id] = model
		}
		return model
	}
	return nil
}

// typeSpecOf 定位类型声明（同包跨文件 / 外部包索引声明）。
func (c *seqPersistCtx) typeSpecOf(ref seqORMTypeRef) (*ast.TypeSpec, *seqFileCtx) {
	if ref.pkgPath == c.pkgPath {
		for _, rel := range seqPackageFilesOf(CodeSequenceRequest{RepoAbs: c.repoAbs}, c.fileRel) {
			ctx := c.fileCtxFor(rel)
			if ctx == nil || ctx.file == nil {
				continue
			}
			if ts := fileTypeSpec(ctx.file, ref.name); ts != nil {
				return ts, ctx
			}
		}
		return nil, nil
	}
	// 外部/其它包：先按索引声明文件，再回退到 go list 选中的包目录（别名等
	// 未单独入索引的声明也能解析）。
	if ctx := c.externalFileCtx("symbol:go:" + ref.pkgPath + ":" + ref.name); ctx != nil {
		if ts := fileTypeSpec(ctx.file, ref.name); ts != nil {
			return ts, ctx
		}
	}
	if c.actions != nil && c.req != nil {
		pkg := seqExternalPackage(c.actions, *c.req, ref.pkgPath)
		if pkg != nil {
			for _, ctx := range pkg.files {
				if ctx == nil || ctx.file == nil {
					continue
				}
				if ts := fileTypeSpec(ctx.file, ref.name); ts != nil {
					return ts, ctx
				}
			}
		}
	}
	return nil, nil
}

// fileTypeSpec 文件级 type 声明。
func fileTypeSpec(f *ast.File, name string) *ast.TypeSpec {
	if f == nil || name == "" {
		return nil
	}
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name != nil && ts.Name.Name == name {
				return ts
			}
		}
	}
	return nil
}

// seqORMStructFields 结构体声明字段（name/go_type/原始 tag/embedded）。
func seqORMStructFields(fset *token.FileSet, src []byte, st *ast.StructType) []SeqORMField {
	if st == nil {
		return nil
	}
	var out []SeqORMField
	for _, fl := range st.Fields.List {
		goType := ""
		if fl.Type != nil {
			start := fset.Position(fl.Type.Pos()).Offset
			end := fset.Position(fl.Type.End()).Offset
			if start >= 0 && end <= len(src) && start < end {
				goType = strings.TrimSpace(string(src[start:end]))
			}
		}
		tag := ""
		if fl.Tag != nil {
			tag = strings.Trim(fl.Tag.Value, "`")
		}
		if len(fl.Names) == 0 {
			out = append(out, SeqORMField{Name: exprTypeName(fl.Type), GoType: goType,
				Tag: tag, Embedded: true})
			continue
		}
		for _, n := range fl.Names {
			out = append(out, SeqORMField{Name: n.Name, GoType: goType, Tag: tag})
		}
	}
	return out
}
