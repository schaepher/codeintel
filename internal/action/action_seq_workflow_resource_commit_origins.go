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

// txCallOrigin 调用表达式槽 0 的事务来源键（直接 Begin / 同包 helper 的唯一
// return；冲突返回或外部源码缺失 → 空，不猜）。
func (a *seqCommitAnalyzer) txCallOrigin(call *ast.CallExpr, depth int) string {
	if call == nil || depth > seqCommitHelperMaxDepth {
		return ""
	}
	if seqIsBeginCall(call) {
		return a.key(a.offset(call), a.rel)
	}
	// 同包 helper：唯一 return 的槽 0 是 Begin / 另一个 helper。
	id, ok := call.Fun.(*ast.Ident)
	if !ok {
		return ""
	}
	fd, file := a.pkgFuncUnique(id.Name)
	if fd == nil || file == nil || fd.Body == nil || file.fset == nil {
		return ""
	}
	var rets []*ast.ReturnStmt
	count := 0
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ReturnStmt:
			count++
			rets = append(rets, v)
			return false
		}
		return true
	})
	if count != 1 || len(rets[0].Results) == 0 {
		return ""
	}
	inner, ok := runtimeUnparen(rets[0].Results[0]).(*ast.CallExpr)
	if !ok {
		return ""
	}
	if seqIsBeginCall(inner) {
		return a.key(file.fset.Position(inner.Lparen).Offset, file.rel)
	}
	return (&seqCommitAnalyzer{fset: file.fset, rel: file.rel, files: a.files}).
		txCallOrigin(inner, depth+1)
}

// pkgFuncUnique 同包唯一同名函数（重名/方法 → nil，不按扫描顺序选）。
func (a *seqCommitAnalyzer) pkgFuncUnique(name string) (*ast.FuncDecl, *seqParsedGoFile) {
	var found *ast.FuncDecl
	var file *seqParsedGoFile
	for _, f := range a.files {
		for _, d := range f.file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || fd.Name == nil || fd.Name.Name != name {
				continue
			}
			if found != nil {
				return nil, nil
			}
			found, file = fd, f
		}
	}
	return found, file
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
