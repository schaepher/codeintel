package action

// U04g：实参静态类型解析（声明类型/字面量/new/单结果构造器/局部赋值）。

import (
	"go/ast"
	"go/token"
)

// seqORMArgTypeRef 实参的静态类型（声明类型/字面量/new/单结果构造器/局部赋值）。
func seqORMArgTypeRef(c *seqPersistCtx, e ast.Expr, depth int) (seqORMTypeRef, bool) {
	if e == nil || depth > seqORMModelMaxDepth {
		return seqORMTypeRef{}, false
	}
	switch v := e.(type) {
	case *ast.ParenExpr:
		return seqORMArgTypeRef(c, v.X, depth+1)
	case *ast.UnaryExpr:
		if v.Op == token.AND {
			return seqORMArgTypeRef(c, v.X, depth+1)
		}
		return seqORMTypeRef{}, false
	case *ast.CompositeLit:
		return seqORMTypeExprRef(c, v.Type, c.file, c.pkgPath, depth+1)
	case *ast.ArrayType:
		// Find(&records)：[]T 的元素类型是模型。
		return seqORMTypeExprRef(c, v.Elt, c.file, c.pkgPath, depth+1)
	case *ast.Ident:
		return seqORMIdentTypeRef(c, v, depth)
	case *ast.CallExpr:
		return seqORMCallTypeRef(c, v, depth)
	}
	return seqORMTypeRef{}, false
}

// seqORMIdentTypeRef 标识符的声明类型（形参/var/短声明；局部赋值递归）。
func seqORMIdentTypeRef(c *seqPersistCtx, id *ast.Ident, depth int) (seqORMTypeRef, bool) {
	if id.Obj == nil || id.Obj.Kind == ast.Pkg {
		return seqORMTypeRef{}, false
	}
	switch d := id.Obj.Decl.(type) {
	case *ast.Field:
		return seqORMTypeExprRef(c, d.Type, c.file, c.pkgPath, depth+1)
	case *ast.ValueSpec:
		if d.Type != nil {
			return seqORMTypeExprRef(c, d.Type, c.file, c.pkgPath, depth+1)
		}
		if len(d.Values) == 1 && len(d.Names) == 1 {
			return seqORMArgTypeRef(c, d.Values[0], depth+1)
		}
	case *ast.AssignStmt:
		// 短声明/赋值：类型来自对应 RHS（多返回值不猜槽）。
		if len(d.Lhs) != len(d.Rhs) {
			return seqORMTypeRef{}, false
		}
		for i, lhs := range d.Lhs {
			if lid, ok := lhs.(*ast.Ident); ok && lid.Obj == id.Obj {
				return seqORMArgTypeRef(c, d.Rhs[i], depth+1)
			}
		}
	}
	return seqORMTypeRef{}, false
}

// seqORMCallTypeRef 调用：未遮蔽 new(T) 或单结果包级构造器的返回签名。
func seqORMCallTypeRef(c *seqPersistCtx, call *ast.CallExpr, depth int) (seqORMTypeRef, bool) {
	if id, ok := call.Fun.(*ast.Ident); ok {
		if id.Name == "new" && id.Obj == nil && len(call.Args) == 1 {
			return seqORMTypeExprRef(c, call.Args[0], c.file, c.pkgPath, depth+1)
		}
		if id.Name == "new" && id.Obj != nil {
			return seqORMTypeRef{}, false // new 被遮蔽
		}
	}
	fn, file, pkgPath, ok := c.resolveFuncDecl(call.Fun)
	if !ok || fn == nil || fn.Type == nil || fn.Type.Results == nil {
		return seqORMTypeRef{}, false
	}
	res := fn.Type.Results
	if len(res.List) != 1 {
		return seqORMTypeRef{}, false
	}
	field := res.List[0]
	if len(field.Names) > 1 {
		return seqORMTypeRef{}, false
	}
	return seqORMTypeExprRef(c, field.Type, file, pkgPath, depth+1)
}

// seqORMTypeExprRef 类型表达式 → 包内/外部类型引用（别名跟随由声明解析完成）。
func seqORMTypeExprRef(c *seqPersistCtx, typ ast.Expr, file *ast.File, pkgPath string, depth int) (seqORMTypeRef, bool) {
	if typ == nil || c == nil || depth > seqORMModelMaxDepth {
		return seqORMTypeRef{}, false
	}
	if star, ok := typ.(*ast.StarExpr); ok {
		return seqORMTypeExprRef(c, star.X, file, pkgPath, depth+1)
	}
	switch t := typ.(type) {
	case *ast.Ident:
		if t.Name == "" {
			return seqORMTypeRef{}, false
		}
		// 局部类型声明不借同名包级声明（对象身份优先）。
		if t.Obj != nil {
			if ts, ok := t.Obj.Decl.(*ast.TypeSpec); ok && seqTypeSpecLocalInFile(file, ts) {
				return seqORMTypeRef{}, false
			}
		} else if !seqFileLevelTypeSpec(file, t.Name) && !seqBuiltinTypeName(t.Name) &&
			!seqPkgDeclaresFileLevelType(c, pkgPath, t.Name) {
			return seqORMTypeRef{}, false
		}
		return seqORMTypeRef{pkgPath: pkgPath, name: t.Name}, true
	case *ast.ArrayType:
		// []T（Find 目标切片）→ 元素模型。
		return seqORMTypeExprRef(c, t.Elt, file, pkgPath, depth+1)
	case *ast.SelectorExpr:
		id, ok := t.X.(*ast.Ident)
		if !ok || t.Sel.Name == "" {
			return seqORMTypeRef{}, false
		}
		p := seqImportPath(file, id.Name)
		if p == "" {
			return seqORMTypeRef{}, false
		}
		return seqORMTypeRef{pkgPath: p, name: t.Sel.Name}, true
	}
	return seqORMTypeRef{}, false
}

// seqPkgDeclaresFileLevelType 包内其它文件是否有该文件级类型声明。
func seqPkgDeclaresFileLevelType(c *seqPersistCtx, pkgPath, name string) bool {
	if c == nil || c.req == nil || pkgPath == "" || name == "" {
		return false
	}
	for _, ctx := range seqPkgFilesForReq(*c.req, pkgPath, "") {
		if ctx == nil || ctx.file == nil || seqFileHasBuildConstraint(ctx) {
			continue
		}
		if seqFileLevelTypeSpec(ctx.file, name) {
			return true
		}
	}
	return false
}

// seqTypeSpecLocalInFile 该 TypeSpec 是否是**当前文件内的局部（函数内）**声明；
// 跨文件包级声明（Obj 指向其它文件）不能因当前文件不同而拒绝。
func seqTypeSpecLocalInFile(f *ast.File, ts *ast.TypeSpec) bool {
	if f == nil || ts == nil {
		return false
	}
	if ts.Pos() < f.Pos() || ts.End() > f.End() {
		return false
	}
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		if ts.Pos() >= fd.Body.Pos() && ts.End() <= fd.Body.End() {
			return true
		}
	}
	return false
}

// seqFileLevelTypeSpecObj 该 TypeSpec 是否为文件级声明。
func seqFileLevelTypeSpecObj(f *ast.File, ts *ast.TypeSpec) bool {
	if f == nil || ts == nil {
		return false
	}
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			if spec == ts {
				return true
			}
		}
	}
	return false
}
