package action

// 资源事实遍历器的声明查找（同包/仓内跨包；从 builder 拆出）。

import (
	"go/ast"
	"path/filepath"
)

// fileOfFunc 函数声明所在文件。
func (b *seqResourceFactBuilder) fileOfFunc(fd *ast.FuncDecl) *seqParsedGoFile {
	for _, f := range b.files {
		for _, d := range f.file.Decls {
			if d == ast.Decl(fd) {
				return f
			}
		}
	}
	return nil
}

// importFunc 仓内导入包函数（外部依赖不加载源码）。
func (b *seqResourceFactBuilder) importFunc(importPath, name string) (*ast.FuncDecl, *seqParsedGoFile) {
	dir, ok := seqRepoPkgDirFor(b.repo, importPath)
	if !ok {
		return nil, nil
	}
	files := seqParsePackageDir(filepath.Join(b.repo, dir))
	var found *ast.FuncDecl
	var foundFile *seqParsedGoFile
	for _, f := range files {
		for _, d := range f.file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || fd.Name == nil || fd.Name.Name != name {
				continue
			}
			if found != nil {
				return nil, nil
			}
			found, foundFile = fd, f
		}
	}
	return found, foundFile
}
