package action

// 提交证明与模块路径辅助（从 resource builder 拆出）。

import (
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// seqCommitGuardedVars 入口内被 `if err = tx.Commit(); err != nil { return }`
// 检查过且函数存在正常 return 的事务变量名。
func seqCommitGuardedVars(fn *ast.FuncDecl) map[string]bool {
	out := map[string]bool{}
	if fn == nil || fn.Body == nil {
		return out
	}
	hasSuccessReturn := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.IfStmt:
			call := seqIfInitCall(v.Init)
			if call == nil || !seqIsCommitCall(call) || !seqCondErrNonNil(v.Cond) ||
				!seqBlockHasFailureReturn(v.Body) {
				break
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				if id, ok := runtimeUnparen(sel.X).(*ast.Ident); ok {
					out[id.Name] = true
				}
			}
		case *ast.ReturnStmt:
			if len(v.Results) == 0 || (len(v.Results) == 1 && seqIsNilIdent(v.Results[0])) {
				hasSuccessReturn = true
			}
		}
		return true
	})
	if !hasSuccessReturn {
		return map[string]bool{}
	}
	return out
}

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

// seqIfInitCall if 初始化语句里的调用（`if err = tx.Commit(); ...`）。
func seqIfInitCall(init ast.Stmt) *ast.CallExpr {
	as, ok := init.(*ast.AssignStmt)
	if !ok || len(as.Rhs) != 1 {
		return nil
	}
	call, _ := as.Rhs[0].(*ast.CallExpr)
	return call
}

// seqIsCommitCall selector 方法名为 Commit。
func seqIsCommitCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Commit"
}

// seqCondErrNonNil `err != nil` / `nil != err`。
func seqCondErrNonNil(e ast.Expr) bool {
	b, ok := runtimeUnparen(e).(*ast.BinaryExpr)
	if !ok || b.Op != token.NEQ {
		return false
	}
	return (seqIsNilIdent(b.Y) && seqIsErrIdent(b.X)) || (seqIsNilIdent(b.X) && seqIsErrIdent(b.Y))
}

func seqIsErrIdent(e ast.Expr) bool {
	id, ok := runtimeUnparen(e).(*ast.Ident)
	return ok && strings.HasPrefix(id.Name, "err")
}

// seqBlockHasFailureReturn 失败分支里返回（return/return err）。
func seqBlockHasFailureReturn(body *ast.BlockStmt) bool {
	if body == nil {
		return false
	}
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if _, ok := n.(*ast.ReturnStmt); ok {
			found = true
			return false
		}
		return true
	})
	return found
}

// seqPkgVarNames 包级 var 名称集合（跨文件）。
func seqPkgVarNames(files map[string]*seqParsedGoFile) map[string]bool {
	out := map[string]bool{}
	for _, f := range files {
		for _, d := range f.file.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				if vs, ok := spec.(*ast.ValueSpec); ok {
					for _, n := range vs.Names {
						out[n.Name] = true
					}
				}
			}
		}
	}
	return out
}
