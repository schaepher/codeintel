package action

// 资源声明解析（从 action_seq_storage.go 拆出）。

import (
	"go/ast"
	"go/token"
)

// seqPackageLevelSQLDBVar 包内是否存在同名包级 *sql.DB 声明（跨文件回退）。
func seqPackageLevelSQLDBVar(c *seqPersistCtx, name string) bool {
	if c == nil || c.req == nil || c.pkgPath == "" || name == "" {
		return false
	}
	for _, ctx := range seqPkgFilesForReq(*c.req, c.pkgPath, "") {
		if ctx == nil || ctx.file == nil {
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
					if n.Name != name || n.Obj == nil {
						continue
					}
					if runtimeIsSQLDBDecl(n, ctx.file) {
						return true
					}
				}
			}
		}
	}
	return false
}

// seqDeclFileFor 声明所在文件（跨文件包级 DB 声明不能因当前文件不同而丢失）。
func seqDeclFileFor(c *seqPersistCtx, vs *ast.ValueSpec) *ast.File {
	if c == nil || vs == nil {
		return nil
	}
	if seqFileLevelVarSpec(c.file, vs) {
		return c.file
	}
	if c.req == nil || c.pkgPath == "" {
		return nil
	}
	for _, ctx := range seqPkgFilesForReq(*c.req, c.pkgPath, "") {
		if ctx != nil && ctx.file != nil && seqFileLevelVarSpec(ctx.file, vs) {
			return ctx.file
		}
	}
	return nil
}
