package action

// B06e：Commit 证明的来源解析（事务来源 / Commit 错误槽 / helper 返回槽）。
//
// 从 action_seq_workflow_resource_commit_dataflow.go 拆出（行数治理）。

import (
	"go/ast"
	"go/token"
	"path/filepath"
)

// exprBind 表达式的事务来源 / Commit 错误槽 / 错误别名。
func (a *seqCommitAnalyzer) exprBind(e ast.Expr, st *seqCommitState) (out struct {
	origin string
	alias  *ast.Object
	commit string
}) {
	e = runtimeUnparen(e)
	switch v := e.(type) {
	case *ast.CallExpr:
		if origin := a.txCallOrigin(v, 0); origin != "" {
			out.origin = origin
			return out
		}
		if seqIsCommitCall(v) {
			// Commit 的错误槽 = 这次调用的实际返回槽；成功检查必须引用
			// 这个变量（不按 err 前缀）。证明身份用 Commit 调用位置 +
			// 事务来源（由调用方按 origin id 绑定）。
			if txObj := receiverIdentObj(v); txObj != nil && st.binds[txObj] != "" {
				out.commit = a.siteKey(a.offset(v), a.rel)
			}
		}
	case *ast.Ident:
		if v.Obj == nil {
			return out
		}
		if origin := st.binds[v.Obj]; origin != "" {
			// 事务变量传递（tx2 := tx）。
			out.origin = origin
			return out
		}
		out.alias = a.aliasRoot(st, v.Obj)
	}
	return out
}

// seqBlockCommitFailurePropagates Commit 失败分支是否真的把失败返回出去：
// `return nil` / 裸 return 但命名返回值不是该错误槽 → 不构成成功证明。
func seqBlockCommitFailurePropagates(body *ast.BlockStmt, st *seqCommitState,
	commitErr *ast.Object, a *seqCommitAnalyzer) bool {
	var rets []*ast.ReturnStmt
	var visit func(stmt ast.Stmt)
	var walk func(stmts []ast.Stmt)
	visit = func(stmt ast.Stmt) {
		switch s := stmt.(type) {
		case *ast.ReturnStmt:
			rets = append(rets, s)
		case *ast.BlockStmt:
			walk(s.List)
		case *ast.IfStmt:
			if s.Init != nil {
				visit(s.Init)
			}
			visit(s.Body)
			if s.Else != nil {
				visit(s.Else)
			}
		case *ast.ForStmt:
			visit(s.Body)
		case *ast.RangeStmt:
			visit(s.Body)
		case *ast.SwitchStmt:
			visit(s.Body)
		case *ast.TypeSwitchStmt:
			visit(s.Body)
		case *ast.SelectStmt:
			visit(s.Body)
		case *ast.CaseClause:
			walk(s.Body)
		case *ast.CommClause:
			walk(s.Body)
		case *ast.LabeledStmt:
			visit(s.Stmt)
		}
	}
	walk = func(stmts []ast.Stmt) {
		for _, stmt := range stmts {
			visit(stmt)
		}
	}
	walk(body.List)
	if len(rets) == 0 {
		return false
	}
	for _, ret := range rets {
		if len(ret.Results) == 0 {
			// 裸 return：只有当某个命名返回值就是本次 Commit 的错误槽时，
			// 失败才会被传播（否则返回零值 = 吞掉错误）。
			ok := false
			if a != nil {
				for slot := 0; ; slot++ {
					obj := seqNamedResultObj(a.fn, slot)
					if obj == nil {
						break
					}
					if a.aliasRoot(st, obj) == commitErr {
						ok = true
						break
					}
				}
			}
			if !ok {
				return false
			}
			continue
		}
		for _, res := range ret.Results {
			if seqIsNilIdent(res) {
				return false // `return nil`：Commit 失败被吞掉。
			}
		}
	}
	return true
}

// offset 调用左括号的字节偏移（事务来源身份）。
func (a *seqCommitAnalyzer) offset(call *ast.CallExpr) int {
	if call == nil || a.fset == nil || !call.Lparen.IsValid() {
		return 0
	}
	return a.fset.Position(call.Lparen).Offset
}

// siteKey 调用位置键（仓库相对 file:offset）。
func (a *seqCommitAnalyzer) siteKey(offset int, rel string) string {
	if offset <= 0 || rel == "" {
		return ""
	}
	return a.relPath(rel) + ":" + itoaSeq(offset)
}

// key 事务来源键（Begin 调用的 file:offset；helper 场景也解析到实际 Begin）。
func (a *seqCommitAnalyzer) key(offset int, rel string) string {
	if offset <= 0 || rel == "" {
		return ""
	}
	return a.relPath(rel) + ":" + itoaSeq(offset)
}

// relPath 包内文件 → 仓库相对路径（与写入证据的 Origin.File 同口径）。
func (a *seqCommitAnalyzer) relPath(rel string) string {
	if a.dir == "" || a.dir == "." {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(filepath.Join(a.dir, rel))
}

// receiverIdentObj 方法调用接收者的变量对象。
func receiverIdentObj(call *ast.CallExpr) *ast.Object {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	id, ok := runtimeUnparen(sel.X).(*ast.Ident)
	if !ok || id.Obj == nil {
		return nil
	}
	return id.Obj
}

// seqIsBeginCall 标准库 Begin/BeginTx 调用（与事务身份同一份方法表）。
func seqIsBeginCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	switch sel.Sel.Name {
	case "Begin", "BeginTx":
		return true
	}
	return false
}

// seqCondNonNilObj `x != nil` / `nil != x` 的变量对象（不按名称前缀判定）。
func seqCondNonNilObj(e ast.Expr) *ast.Object {
	b, ok := runtimeUnparen(e).(*ast.BinaryExpr)
	if !ok || b.Op != token.NEQ {
		return nil
	}
	var other ast.Expr
	switch {
	case seqIsNilIdent(b.Y):
		other = b.X
	case seqIsNilIdent(b.X):
		other = b.Y
	default:
		return nil
	}
	id, ok := runtimeUnparen(other).(*ast.Ident)
	if !ok || id.Obj == nil || id.Name == "_" {
		return nil
	}
	return id.Obj
}

// seqBlockHasFailureReturnScoped 失败分支里直接返回（不穿透嵌套函数字面量）。
func seqBlockHasFailureReturnScoped(body *ast.BlockStmt) bool {
	if body == nil {
		return false
	}
	found := false
	var visit func(st ast.Stmt)
	var walk func(stmts []ast.Stmt)
	visit = func(st ast.Stmt) {
		switch s := st.(type) {
		case *ast.ReturnStmt:
			found = true
		case *ast.BlockStmt:
			walk(s.List)
		case *ast.IfStmt:
			if s.Init != nil {
				visit(s.Init)
			}
			visit(s.Body)
			if s.Else != nil {
				visit(s.Else)
			}
		case *ast.ForStmt:
			visit(s.Body)
		case *ast.RangeStmt:
			visit(s.Body)
		case *ast.SwitchStmt:
			visit(s.Body)
		case *ast.TypeSwitchStmt:
			visit(s.Body)
		case *ast.SelectStmt:
			visit(s.Body)
		case *ast.LabeledStmt:
			visit(s.Stmt)
		}
	}
	walk = func(stmts []ast.Stmt) {
		for _, st := range stmts {
			if found {
				return
			}
			visit(st)
		}
	}
	walk(body.List)
	return found
}
