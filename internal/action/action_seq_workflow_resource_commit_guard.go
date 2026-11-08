package action

// B06e.3：Commit 成功检查的守卫判定（失败分支必须真的传播错误）。

import "go/ast"

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
	if !seqBlockCommitFailurePropagates(body, st, obj, a) {
		return "", false
	}
	return key, true
}
