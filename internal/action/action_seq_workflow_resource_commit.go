package action

// 提交证明与模块路径辅助（从 resource builder 拆出）。
//
// B06e 的数据流分析在 action_seq_workflow_resource_commit_dataflow.go；
// 这里只保留 AST 谓词与包级声明辅助。

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"strings"
)

// seqIsCommitCall selector 方法名为 Commit。
func seqIsCommitCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Commit"
}

// seqCondErrNonNil `err != nil` / `nil != err`（旧口径：按 err 前缀；B06e 的
// 提交证明改用 seqCondNonNilObj 比较实际错误槽，这里只服务其它读取方）。
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

// filepathBase 事务来源键里的文件身份（同包内 basename 唯一）。
func filepathBase(p string) string {
	return filepath.Base(p)
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

// seqPkgVarObjects 包级 var 的 AST 对象集合（声明身份）。
func seqPkgVarObjects(files map[string]*seqParsedGoFile) map[*ast.Object]bool {
	out := map[*ast.Object]bool{}
	for _, f := range files {
		for _, d := range f.file.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				if vs, ok := spec.(*ast.ValueSpec); ok {
					for _, n := range vs.Names {
						if n.Obj != nil {
							out[n.Obj] = true
						}
					}
				}
			}
		}
	}
	return out
}
