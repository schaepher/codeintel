package action

// 模块 import path → 仓库目录（从 resource commit 拆出）。

import (
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// seqRepoPkgDirFor 模块内 import path → 仓库相对目录。
func seqRepoPkgDirFor(repo, importPath string) (string, bool) {
	mod := seqReadModulePath(repo)
	if mod == "" || importPath == "" {
		return "", false
	}
	switch {
	case importPath == mod:
		return "", true
	case strings.HasPrefix(importPath, mod+"/"):
		return strings.TrimPrefix(importPath, mod+"/"), true
	}
	return "", false
}

// seqReadModulePath 读取仓库根 go.mod 的 module 路径（带缓存）。
func seqReadModulePath(repo string) string {
	if v, ok := seqModulePathCache.Load(repo); ok {
		s, _ := v.(string)
		return s
	}
	data, err := os.ReadFile(filepath.Join(repo, "go.mod"))
	path := ""
	if err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "module ") {
				path = strings.TrimSpace(strings.TrimPrefix(line, "module "))
				break
			}
		}
	}
	seqModulePathCache.Store(repo, path)
	return path
}

// walkDeclClosures var f = func(){...} 记录闭包变量。
func (b *seqResourceFactBuilder) walkDeclClosures(s *ast.DeclStmt, file *seqParsedGoFile,
	pos int, isEntry bool, depth int, closures map[*ast.Object]*ast.FuncLit) {
	gd, ok := s.Decl.(*ast.GenDecl)
	if !ok || gd.Tok != token.VAR {
		return
	}
	for _, spec := range gd.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for i, n := range vs.Names {
			if i >= len(vs.Values) {
				continue
			}
			if lit, ok := runtimeUnparen(vs.Values[i]).(*ast.FuncLit); ok && n.Obj != nil {
				closures[n.Obj] = lit
			}
		}
	}
}

// seqStaticCond 条件静态真假：1 真 / -1 假 / 0 未知。
func seqStaticCond(e ast.Expr) int {
	if e == nil {
		return 0
	}
	switch v := runtimeUnparen(e).(type) {
	case *ast.Ident:
		if v.Obj != nil {
			return 0
		}
		if v.Name == "true" {
			return 1
		}
		if v.Name == "false" {
			return -1
		}
	case *ast.UnaryExpr:
		if v.Op == token.NOT {
			switch seqStaticCond(v.X) {
			case 1:
				return -1
			case -1:
				return 1
			}
		}
	}
	return 0
}
