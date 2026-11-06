//go:build integration

package integration

// todo34：B06e/U04bU/B03e/B09e/B10e/B11e（源码 → init → CLI）。

import (
	"encoding/json"
	"testing"
)

// u34Query 入口 → 关联入口集合的代码级时序 JSON。
func u34Query(t *testing.T, dir, entry string, extra ...string) map[string]any {
	t.Helper()
	args := []string{"query", "sequence", "symbol:go:example.com/remaining:" + entry,
		"--repo", dir, "--code", "--depth", "12", "--json"}
	args = append(args, extra...)
	code, out := runCLIOut(t, args...)
	if code != 0 {
		t.Fatalf("%s exit=%d\n%s", entry, code, out)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("%s JSON: %v\n%s", entry, err, out)
	}
	return root
}

// u34Conn 写入入口 → 读取入口的连接（没有返回 nil）。
func u34Conn(root map[string]any, from, to string) map[string]any {
	wf, _ := root["workflow"].(map[string]any)
	for _, raw := range asAnySlice(wf["connections"]) {
		c, _ := raw.(map[string]any)
		f, _ := c["from"].(map[string]any)
		tt, _ := c["to"].(map[string]any)
		if seqLastColon(f["entry"]) == from && seqLastColon(tt["entry"]) == to {
			return c
		}
	}
	return nil
}

// u34ConnCount 连接数量。
func u34ConnCount(root map[string]any) int {
	wf, _ := root["workflow"].(map[string]any)
	return len(asAnySlice(wf["connections"]))
}

// u34Commit 连接的成功条件与 transaction_commit 缺失证明。
func u34Commit(t *testing.T, root map[string]any, from, to string) (int, bool, bool) {
	t.Helper()
	wf, _ := root["workflow"].(map[string]any)
	c := u34Conn(root, from, to)
	if c == nil {
		return u34ConnCount(root), false, false
	}
	_ = wf
	commitSuccess := false
	for _, v := range asAnySlice(c["success_conditions"]) {
		if asString(v) == "commit_success" {
			commitSuccess = true
		}
	}
	txMissing := false
	for _, v := range asAnySlice(c["missing_proofs"]) {
		if asString(v) == "transaction_commit" {
			txMissing = true
		}
	}
	return u34ConnCount(root), commitSuccess, txMissing
}

// B06e.1 事务分配：同一声明先后持有不同事务。
func TestCommitAllocationIdentity(t *testing.T) {
	dir := u34Repo(t)
	// 正例：原事务（含别名）写入并检查同一事务的 Commit。
	for _, entry := range []string{"SubmitTx", "SubmitTxAlias"} {
		n, ok, missing := u34Commit(t, u34Query(t, dir, entry, "--related-entry",
			"symbol:go:example.com/remaining:Review"), entry, "Review")
		if n != 1 || !ok || missing {
			t.Fatalf("positive %s = %d/%v/%v（want 1/true/false）", entry, n, ok, missing)
		}
	}
	// 反例：重绑到另一事务（原写入不能被新事务的 Commit 证明）。
	for _, entry := range []string{"SubmitReboundTx", "SubmitAliasRebind"} {
		n, ok, missing := u34Commit(t, u34Query(t, dir, entry, "--related-entry",
			"symbol:go:example.com/remaining:Review"), entry, "Review")
		if n != 1 || ok || !missing {
			t.Fatalf("negative %s = %d/%v/%v（want 1/false/true）", entry, n, ok, missing)
		}
	}
}

// B06e.2 错误槽：检查的必须是本次 Commit 的实际错误（含别名）。
func TestCommitErrorSlotIdentity(t *testing.T) {
	dir := u34Repo(t)
	for _, entry := range []string{"SubmitTx", "SubmitErrAlias"} {
		n, ok, missing := u34Commit(t, u34Query(t, dir, entry, "--related-entry",
			"symbol:go:example.com/remaining:Review"), entry, "Review")
		if n != 1 || !ok || missing {
			t.Fatalf("positive %s = %d/%v/%v（want 1/true/false）", entry, n, ok, missing)
		}
	}
	// 反例：检查无关错误（errOther）或覆盖错误后再检查。
	for _, entry := range []string{"SubmitWrongError", "SubmitOverwriteError"} {
		n, ok, missing := u34Commit(t, u34Query(t, dir, entry, "--related-entry",
			"symbol:go:example.com/remaining:Review"), entry, "Review")
		if n != 1 || ok || !missing {
			t.Fatalf("negative %s = %d/%v/%v（want 1/false/true）", entry, n, ok, missing)
		}
	}
}

// B06e.3 正常出口：所有成功出口都经过同一检查才成立。
func TestCommitExitPaths(t *testing.T) {
	dir := u34Repo(t)
	n, ok, missing := u34Commit(t, u34Query(t, dir, "SubmitTx", "--related-entry",
		"symbol:go:example.com/remaining:Review"), "SubmitTx", "Review")
	if n != 1 || !ok || missing {
		t.Fatalf("positive SubmitTx = %d/%v/%v", n, ok, missing)
	}
	// 反例：条件绕过、只 Rollback、未执行闭包里的 Commit、失败分支无直接 return。
	for _, entry := range []string{"SubmitConditionalCommit", "SubmitRollbackOnly",
		"SubmitClosureCommit", "SubmitNoDirectReturn"} {
		n, ok, missing := u34Commit(t, u34Query(t, dir, entry, "--related-entry",
			"symbol:go:example.com/remaining:Review"), entry, "Review")
		if ok || !missing {
			t.Fatalf("negative %s = %d/%v/%v（want false/true）", entry, n, ok, missing)
		}
	}
}

// B06e.4 跨函数：沿返回槽证明实际 Commit；另一事务/冲突返回不能证明。
func TestCommitInterproceduralOrigins(t *testing.T) {
	dir := u34Repo(t)
	root := u34Query(t, dir, "SubmitHelperTx", "--related-entry",
		"symbol:go:example.com/remaining:Review")
	n, ok, missing := u34Commit(t, root, "SubmitHelperTx", "Review")
	if n != 1 || !ok || missing {
		t.Fatalf("positive SubmitHelperTx = %d/%v/%v（want 1/true/false）", n, ok, missing)
	}
	for _, entry := range []string{"SubmitHelperSplit", "SubmitHelperConflict"} {
		root := u34Query(t, dir, entry, "--related-entry",
			"symbol:go:example.com/remaining:Review")
		n, ok, missing := u34Commit(t, root, entry, "Review")
		if ok || !missing {
			t.Fatalf("negative %s = %d/%v/%v（want false/true）", entry, n, ok, missing)
		}
	}
}

// U04bU.1 循环可达性：零次循环不产生改写。
func TestResourceLoopPaths(t *testing.T) {
	dir := u34Repo(t)
	pos := u34Query(t, dir, "Submit", "--related-entry",
		"symbol:go:example.com/remaining:ReviewZeroLoop")
	if n := u34ConnCount(pos); n != 1 {
		t.Fatalf("positive ReviewZeroLoop connections = %d（want 1）", n)
	}
	for _, entry := range []string{"ReviewLoopOnce", "ReviewLoopInit", "ReviewLoopPost"} {
		neg := u34Query(t, dir, "Submit", "--related-entry",
			"symbol:go:example.com/remaining:"+entry)
		if n := u34ConnCount(neg); n != 0 {
			t.Fatalf("negative %s connections = %d（want 0）", entry, n)
		}
	}
}

// U04bU.2 终止路径：已返回路径的改写不污染继续读取的路径。
func TestResourceTerminatedBranches(t *testing.T) {
	dir := u34Repo(t)
	pos := u34Query(t, dir, "Submit", "--related-entry",
		"symbol:go:example.com/remaining:ReviewEarlyReset")
	if n := u34ConnCount(pos); n != 1 {
		t.Fatalf("positive ReviewEarlyReset connections = %d（want 1）", n)
	}
	neg := u34Query(t, dir, "Submit", "--related-entry",
		"symbol:go:example.com/remaining:ReviewResetNoReturn")
	if n := u34ConnCount(neg); n != 0 {
		t.Fatalf("negative ReviewResetNoReturn connections = %d（want 0）", n)
	}
}

// U04bU.3 身份及调用位置：调用后改写/未执行闭包保留连接；调用前改写/执行
// 闭包使旧绑定失效。
func TestResourceInvocationIdentity(t *testing.T) {
	dir := u34Repo(t)
	for _, entry := range []string{"ReviewAfterReset", "ReviewUncalledClosure", "ReviewShadow"} {
		root := u34Query(t, dir, "Submit", "--related-entry",
			"symbol:go:example.com/remaining:"+entry)
		if n := u34ConnCount(root); n != 1 {
			t.Fatalf("positive %s connections = %d（want 1）", entry, n)
		}
	}
	for _, entry := range []string{"ReviewBeforeReset", "ReviewCalledClosure"} {
		root := u34Query(t, dir, "Submit", "--related-entry",
			"symbol:go:example.com/remaining:"+entry)
		if n := u34ConnCount(root); n != 0 {
			t.Fatalf("negative %s connections = %d（want 0）", entry, n)
		}
	}
}

// U04bU.4 失败与预算：已证明连接指向实际证据节点；失效后不升级。
func TestResourceFailureContracts(t *testing.T) {
	dir := u34Repo(t)
	root := u34Query(t, dir, "Submit", "--related-entry",
		"symbol:go:example.com/remaining:Review")
	c := u34Conn(root, "Submit", "Review")
	if c == nil {
		t.Fatal("缺少 Submit → Review 连接")
	}
	for _, side := range []string{"from", "to"} {
		ep, _ := c[side].(map[string]any)
		if asString(ep["path"]) == "" || asString(ep["file"]) == "" || ep["line"] == nil {
			t.Fatalf("%s 端点缺少实际证据位置: %v", side, ep)
		}
	}
	// 资源已失效的读取不能被当成"未配置资源"升级成连接。
	if n := u34ConnCount(u34Query(t, dir, "Submit", "--related-entry",
		"symbol:go:example.com/remaining:ReviewBeforeReset")); n != 0 {
		t.Fatalf("失效资源 connections = %d（want 0）", n)
	}
}
