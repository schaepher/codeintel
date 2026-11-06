package action

// 提交证明与模块路径辅助（从 resource builder 拆出）。

import (
	"go/ast"
	"go/token"
	"strings"
)

// seqCommitSuccessVars 路径敏感证明：事务变量的 Commit 错误检查必须出现在
// 所有正常成功出口之前（条件 Commit/遗漏出口都不算）。返回声明行 → true。
func seqCommitSuccessVars(fn *ast.FuncDecl, file *seqParsedGoFile) map[int]bool {
	out := map[int]bool{}
	if fn == nil || fn.Body == nil || file == nil {
		return out
	}
	type state map[int]bool
	clone := func(st state) state {
		cp := state{}
		for k := range st {
			cp[k] = true
		}
		return cp
	}
	var exits []state
	var walkStmts func(stmts []ast.Stmt, st state)
	var walkStmt func(st ast.Stmt, cur state)
	walkStmt = func(st ast.Stmt, cur state) {
		switch s := st.(type) {
		case *ast.ReturnStmt:
			if len(s.Results) == 0 ||
				(len(s.Results) == 1 && seqIsNilIdent(s.Results[0])) {
				exits = append(exits, clone(cur))
			}
		case *ast.BlockStmt:
			walkStmts(s.List, cur)
		case *ast.IfStmt:
			if s.Init != nil {
				walkStmt(s.Init, cur)
			}
			// 本 if 自身是 Commit 成功检查时，成功延续路径标记已检查。
			if call := seqIfInitCall(s.Init); call != nil && seqIsCommitCall(call) &&
				seqCondErrNonNil(s.Cond) && seqBlockHasFailureReturn(s.Body) {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					if id, ok := runtimeUnparen(sel.X).(*ast.Ident); ok && id.Obj != nil {
						line := file.fset.Position(seqDeclPosOf(id.Obj.Decl)).Offset
						next := clone(cur)
						next[line] = true
						if s.Else != nil {
							walkStmt(s.Else, next)
						}
						return // 成功延续在 if 之后由调用方继续
					}
				}
			}
			switch seqStaticCond(s.Cond) {
			case 1:
				walkStmt(s.Body, cur)
			case -1:
				if s.Else != nil {
					walkStmt(s.Else, cur)
				}
			default:
				walkStmt(s.Body, cur)
				if s.Else != nil {
					walkStmt(s.Else, cur)
				}
			}
		case *ast.ForStmt:
			// 循环可能 0 次：体内路径与跳过路径都存在。
			walkStmt(s.Body, cur)
		case *ast.RangeStmt:
			walkStmt(s.Body, cur)
		case *ast.SwitchStmt:
			walkStmt(s.Body, cur)
		case *ast.TypeSwitchStmt:
			walkStmt(s.Body, cur)
		case *ast.SelectStmt:
			walkStmt(s.Body, cur)
		case *ast.DeferStmt, *ast.GoStmt:
			// 延迟/异步执行不构成当前成功路径。
		}
	}
	walkStmts = func(stmts []ast.Stmt, st state) {
		for _, stmt := range stmts {
			// Commit guard 的 if 之后的路径要带上已检查状态。
			if ifs, ok := stmt.(*ast.IfStmt); ok {
				if call := seqIfInitCall(ifs.Init); call != nil && seqIsCommitCall(call) &&
					seqCondErrNonNil(ifs.Cond) && seqBlockHasFailureReturn(ifs.Body) {
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
						if id, ok := runtimeUnparen(sel.X).(*ast.Ident); ok && id.Obj != nil {
							line := file.fset.Position(seqDeclPosOf(id.Obj.Decl)).Offset
							next := clone(st)
							next[line] = true
							if ifs.Else != nil {
								walkStmt(ifs.Else, next)
							}
							st = next
							continue
						}
					}
				}
			}
			walkStmt(stmt, st)
		}
	}
	walkStmts(fn.Body.List, state{})
	// 同一事务变量只有"所有正常出口都已检查"才成立。
	txLines := map[int]bool{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		ifs, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}
		call := seqIfInitCall(ifs.Init)
		if call == nil || !seqIsCommitCall(call) || !seqCondErrNonNil(ifs.Cond) ||
			!seqBlockHasFailureReturn(ifs.Body) {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			if id, ok := runtimeUnparen(sel.X).(*ast.Ident); ok && id.Obj != nil {
				txLines[file.fset.Position(seqDeclPosOf(id.Obj.Decl)).Offset] = true
			}
		}
		return true
	})
	if len(exits) == 0 {
		return out
	}
	for line := range txLines {
		all := true
		for _, st := range exits {
			if !st[line] {
				all = false
				break
			}
		}
		if all {
			out[line] = true
		}
	}
	return out
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
