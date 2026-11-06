package action

// B06e：Commit 证明的数据流分析（事务来源 + Commit 实际错误槽 + 出口）。
//
// 与旧实现相比：
//   - 事务身份 = 产生该事务的 Begin 调用（file:offset），不是变量声明偏移
//     （同一声明可以先持有事务 A、重绑后持有事务 B）；
//   - 被检查的错误必须是**这次 Commit 返回的实际错误槽**（Init LHS 或
//     Commit 赋值的 LHS），别名沿赋值链解析，不以 `err` 前缀判定；
//   - 只有所有正常出口都经过同一成功检查才成立。

import (
	"go/ast"
	"go/token"
)

// seqCommitState 一个程序点上的证明状态（分支克隆后互不影响）。
type seqCommitState struct {
	checked   map[string]bool        // 已证明的 Commit 调用位置 → true
	binds     map[*ast.Object]string // 事务变量 → 事务来源键（空 = 未知）
	commitErr map[*ast.Object]string // Commit 错误变量 → Commit 调用位置键
	alias     map[*ast.Object]*ast.Object
}

func newSeqCommitState() *seqCommitState {
	return &seqCommitState{checked: map[string]bool{}, binds: map[*ast.Object]string{},
		commitErr: map[*ast.Object]string{}, alias: map[*ast.Object]*ast.Object{}}
}

func (st *seqCommitState) clone() *seqCommitState {
	cp := newSeqCommitState()
	for k, v := range st.checked {
		cp.checked[k] = v
	}
	for k, v := range st.binds {
		cp.binds[k] = v
	}
	for k, v := range st.commitErr {
		cp.commitErr[k] = v
	}
	for k, v := range st.alias {
		cp.alias[k] = v
	}
	return cp
}

// aliasRoot 错误变量别名链的根（无别名返回自身；环/未知名返回 nil）。
func (a *seqCommitAnalyzer) aliasRoot(st *seqCommitState, obj *ast.Object) *ast.Object {
	for i := 0; i < 8 && obj != nil; i++ {
		next, ok := st.alias[obj]
		if !ok {
			return obj
		}
		if next == obj {
			return obj
		}
		obj = next
	}
	return nil
}

// seqCommitAnalyzer 单个入口函数的提交证明遍历器。
type seqCommitAnalyzer struct {
	fset  *token.FileSet
	rel   string
	files map[string]*seqParsedGoFile
	exits []*seqCommitState
}

// seqCommitHelperMaxDepth helper 返回事务来源的解析深度（有界）。
const seqCommitHelperMaxDepth = 4

// seqCommitSuccessVars 路径敏感证明：返回事务来源键（file:beginOffset）→ 已证明。
//
// B06e.4：同包 helper 的唯一 return 是 Begin 调用（或另一个这样的 helper）时，
// `tx, err := openTx()` 的事务来源按**实际 Begin 调用**解析（不按变量名）。
func seqCommitSuccessVars(fn *ast.FuncDecl, file *seqParsedGoFile,
	files map[string]*seqParsedGoFile) map[string]bool {
	out := map[string]bool{}
	if fn == nil || fn.Body == nil || file == nil {
		return out
	}
	a := &seqCommitAnalyzer{fset: file.fset, rel: file.rel, files: files}
	a.walkStmts(fn.Body.List, newSeqCommitState())
	if len(a.exits) == 0 {
		return out
	}
	union := map[string]bool{}
	for _, ex := range a.exits {
		for k := range ex.checked {
			union[k] = true
		}
	}
	for key := range union {
		all := true
		for _, ex := range a.exits {
			if !ex.checked[key] {
				all = false
				break
			}
		}
		if all {
			out[key] = true
		}
	}
	return out
}

// walkStmts 顺序遍历块；返回块末尾状态（调用方只在函数顶层使用）。
func (a *seqCommitAnalyzer) walkStmts(stmts []ast.Stmt, st *seqCommitState) *seqCommitState {
	for _, stmt := range stmts {
		st = a.walkStmt(stmt, st)
	}
	return st
}

// walkStmt 单条语句；返回该语句之后的**成功延续**状态。
func (a *seqCommitAnalyzer) walkStmt(stmt ast.Stmt, st *seqCommitState) *seqCommitState {
	switch s := stmt.(type) {
	case nil:
		return st
	case *ast.ReturnStmt:
		if len(s.Results) == 0 || (len(s.Results) == 1 && seqIsNilIdent(s.Results[0])) {
			a.exits = append(a.exits, st.clone())
		}
		return st
	case *ast.BlockStmt:
		// 块内标记不外传（if/for 分支的检查不构成后续语句的成功路径）。
		a.walkStmts(s.List, st.clone())
		return st
	case *ast.AssignStmt:
		a.applyAssign(s, st)
		return st
	case *ast.DeclStmt:
		a.applyDecl(s, st)
		return st
	case *ast.IfStmt:
		if s.Init != nil {
			st = a.walkStmt(s.Init, st)
		}
		if key, ok := a.commitGuard(s.Cond, s.Body, st); ok {
			next := st.clone()
			next.checked[key] = true
			if s.Else != nil {
				a.walkStmt(s.Else, next)
			}
			return next
		}
		switch seqStaticCond(s.Cond) {
		case 1:
			a.walkStmt(s.Body, st.clone())
		case -1:
			if s.Else != nil {
				a.walkStmt(s.Else, st.clone())
			}
		default:
			a.walkStmt(s.Body, st.clone())
			if s.Else != nil {
				a.walkStmt(s.Else, st.clone())
			}
		}
		return st
	case *ast.ForStmt:
		a.walkStmt(s.Body, st.clone())
		return st
	case *ast.RangeStmt:
		a.walkStmt(s.Body, st.clone())
		return st
	case *ast.SwitchStmt:
		a.walkStmt(s.Body, st.clone())
		return st
	case *ast.TypeSwitchStmt:
		a.walkStmt(s.Body, st.clone())
		return st
	case *ast.SelectStmt:
		a.walkStmt(s.Body, st.clone())
		return st
	case *ast.DeferStmt, *ast.GoStmt:
		// 延迟/异步提交不构成当前成功路径（需证明最终错误出口）。
		return st
	}
	return st
}

// commitGuard 该 if 是否检查了某次 Commit 的实际错误并让失败返回。
func (a *seqCommitAnalyzer) commitGuard(cond ast.Expr, body *ast.BlockStmt,
	st *seqCommitState) (string, bool) {
	obj := seqCondNonNilObj(cond)
	if obj == nil {
		return "", false
	}
	obj = a.aliasRoot(st, obj)
	if obj == nil {
		return "", false
	}
	key, ok := st.commitErr[obj]
	if !ok || key == "" {
		return "", false
	}
	if !seqBlockHasFailureReturnScoped(body) {
		return "", false
	}
	return key, true
}

// applyAssign 赋值/短声明：事务绑定、Commit 错误槽与错误别名。
func (a *seqCommitAnalyzer) applyAssign(s *ast.AssignStmt, st *seqCommitState) {
	type bind struct {
		origin string
		alias  *ast.Object
		commit string
	}
	vals := make([]bind, len(s.Lhs))
	for i, lhs := range s.Lhs {
		id, ok := lhs.(*ast.Ident)
		if !ok || id.Obj == nil || id.Name == "_" {
			continue
		}
		if len(s.Rhs) == len(s.Lhs) {
			vals[i] = a.exprBind(s.Rhs[i], st)
			continue
		}
		if len(s.Rhs) == 1 && i == 0 {
			// `tx, err := DB.BeginTx(...)` / `tx, err := openTx(...)`：槽 0 是事务。
			if call, ok := runtimeUnparen(s.Rhs[0]).(*ast.CallExpr); ok {
				vals[i].origin = a.txCallOrigin(call, 0)
			}
		}
	}
	for i, lhs := range s.Lhs {
		id, ok := lhs.(*ast.Ident)
		if !ok || id.Obj == nil || id.Name == "_" {
			continue
		}
		// 任何赋值都覆盖旧来源（不确定来源 → 未知）。
		delete(st.binds, id.Obj)
		delete(st.commitErr, id.Obj)
		delete(st.alias, id.Obj)
		if vals[i].origin != "" {
			st.binds[id.Obj] = vals[i].origin
		}
		if vals[i].commit != "" {
			st.commitErr[id.Obj] = vals[i].commit
		}
		if vals[i].alias != nil && vals[i].alias != id.Obj {
			st.alias[id.Obj] = vals[i].alias
		}
	}
}

// applyDecl `var tx = ...` 声明（与赋值同语义）。
func (a *seqCommitAnalyzer) applyDecl(s *ast.DeclStmt, st *seqCommitState) {
	gd, ok := s.Decl.(*ast.GenDecl)
	if !ok || gd.Tok != token.VAR {
		return
	}
	for _, spec := range gd.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for i, name := range vs.Names {
			if name.Obj == nil || i >= len(vs.Values) {
				continue
			}
			b := a.exprBind(vs.Values[i], st)
			delete(st.binds, name.Obj)
			delete(st.commitErr, name.Obj)
			delete(st.alias, name.Obj)
			if b.origin != "" {
				st.binds[name.Obj] = b.origin
			}
			if b.commit != "" {
				st.commitErr[name.Obj] = b.commit
			}
			if b.alias != nil && b.alias != name.Obj {
				st.alias[name.Obj] = b.alias
			}
		}
	}
}
