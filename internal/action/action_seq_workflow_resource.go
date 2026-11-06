package action

// U04bR：资源绑定失效与 B06b 提交成功路径约束的源码证明。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// seqWorkflowFacts 入口级源码事实（资源改写 / 显式 Commit 成功路径）。
type seqWorkflowFacts struct {
	mutated map[string]map[string]bool // entry → resource symbol → true
	commit  map[string]bool            // entry → commit_success 成立
}

// seqEntryFacts 解析入口函数及其本地调用链（深度 ≤4）。
func seqEntryFacts(a *Actions, repo, entry string) (map[string]bool, bool) {
	mut := map[string]bool{}
	if a == nil || entry == "" {
		return mut, false
	}
	n, err := a.repo.GetSymbol(domain.CanonicalID(entry))
	if err != nil || n.FilePath == "" {
		return mut, false
	}
	path := filepath.Join(repo, n.FilePath)
	src, err := os.ReadFile(path)
	if err != nil {
		return mut, false
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, n.FilePath, src, 0)
	if err != nil {
		return mut, false
	}
	fn := findFuncByLine(f, fset, n.LineStart)
	if fn == nil || fn.Body == nil {
		return mut, false
	}
	pkgVars := seqPackageVarNames(f)
	pkgVarNames := seqPackageVarNameSet(filepath.Dir(path))
	pkg := pkgOfID(domain.CanonicalID(entry))
	visited := map[*ast.FuncDecl]bool{}
	var inspect func(fd *ast.FuncDecl, depth int)
	inspect = func(fd *ast.FuncDecl, depth int) {
		if fd == nil || fd.Body == nil || depth > 4 || visited[fd] {
			return
		}
		visited[fd] = true
		ast.Inspect(fd.Body, func(node ast.Node) bool {
			switch v := node.(type) {
			case *ast.AssignStmt:
				for _, lhs := range v.Lhs {
					if id, ok := runtimeUnparen(lhs).(*ast.Ident); ok {
						if (id.Obj != nil && pkgVars[id.Obj]) ||
							(id.Obj == nil && pkgVarNames[id.Name]) {
							mut["symbol:go:"+pkg+":"+id.Name] = true
						}
					}
				}
			case *ast.IncDecStmt:
				if id, ok := runtimeUnparen(v.X).(*ast.Ident); ok {
					if (id.Obj != nil && pkgVars[id.Obj]) ||
						(id.Obj == nil && pkgVarNames[id.Name]) {
						mut["symbol:go:"+pkg+":"+id.Name] = true
					}
				}
			case *ast.CallExpr:
				// 同包本地函数（含包级函数）递归展开其体内副作用。
				if id, ok := v.Fun.(*ast.Ident); ok {
					if decl, count := findFuncDeclInFile(f, id.Name); count == 1 && decl != nil {
						inspect(decl, depth+1)
					}
				}
			}
			return true
		})
	}
	inspect(fn, 0)
	return mut, seqFuncHasCommitSuccess(fn)
}

// seqPackageVarNames 文件里的包级 var 对象集合。
func seqPackageVarNames(f *ast.File) map[*ast.Object]bool {
	out := map[*ast.Object]bool{}
	if f == nil {
		return out
	}
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, name := range vs.Names {
				if name.Obj != nil {
					out[name.Obj] = true
				}
			}
		}
	}
	return out
}

// seqPackageVarNameSet 目录内所有 Go 文件的包级 var 名称集合。
func seqPackageVarNameSet(dir string) map[string]bool {
	out := map[string]bool{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
		if err != nil {
			continue
		}
		for _, d := range f.Decls {
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

// seqFuncHasCommitSuccess 函数里是否存在"显式检查 Commit 错误并失败返回"的模式：
//
//	if err = tx.Commit(); err != nil { return err }
//	if err := tx.Commit(); err != nil { return }
//
// 只有这种失败路径 + 正常返回路径同时存在，才认为源入口成功返回携带
// commit_success（仍不证明真实提交发生）。
func seqFuncHasCommitSuccess(fn *ast.FuncDecl) bool {
	if fn == nil || fn.Body == nil {
		return false
	}
	found := false
	hasSuccessReturn := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.IfStmt:
			if v.Cond == nil || v.Body == nil {
				break
			}
			call := seqIfInitCall(v.Init)
			if call == nil || !seqIsCommitCall(call) {
				break
			}
			if !seqCondErrNonNil(v.Cond) {
				break
			}
			if seqBlockHasFailureReturn(v.Body) {
				found = true
			}
		case *ast.ReturnStmt:
			if len(v.Results) == 0 || (len(v.Results) == 1 && seqIsNilIdent(v.Results[0])) {
				hasSuccessReturn = true
			}
		}
		return true
	})
	return found && hasSuccessReturn
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
