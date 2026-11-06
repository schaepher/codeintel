package action

// 跨文件 xorm.Session 句柄解析（从 action_seq_orm.go 拆出）。

import (
	"go/ast"
	"go/token"
)

// seqPkgLevelXormSession 包内是否存在同名 `var X *xorm.Session` 声明。
func seqPkgLevelXormSession(c *seqPersistCtx, name string) bool {
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
				for i, n := range vs.Names {
					if n.Name != name {
						continue
					}
					var typ ast.Expr
					if i < len(vs.Names) && vs.Type != nil {
						typ = vs.Type
					} else if len(vs.Values) == 1 && vs.Type == nil {
						continue
					}
					if star, ok := typ.(*ast.StarExpr); ok {
						typ = star.X
					}
					sel, ok := typ.(*ast.SelectorExpr)
					if !ok || sel.Sel.Name != "Session" {
						continue
					}
					pkgID, ok := sel.X.(*ast.Ident)
					if !ok {
						continue
					}
					if seqXormPkgPaths[seqImportPath(ctx.file, pkgID.Name)] {
						return true
					}
				}
			}
		}
	}
	return false
}
