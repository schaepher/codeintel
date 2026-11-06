//go:build integration

package integration

// todo32：B06c/U04bS/B09c/B03c/B10c/B11c（源码 → init → CLI）。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func u32Query(t *testing.T, dir, entry string, extra ...string) map[string]any {
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

func u32Conn(wf map[string]any, from, to string) map[string]any {
	conns, _ := wf["connections"].([]any)
	for _, raw := range conns {
		c, _ := raw.(map[string]any)
		f, _ := c["from"].(map[string]any)
		tt, _ := c["to"].(map[string]any)
		if seqLastColon(f["entry"]) == from && seqLastColon(tt["entry"]) == to {
			return c
		}
	}
	return nil
}

func u32ConnCount(wf map[string]any) int {
	conns, _ := wf["connections"].([]any)
	return len(conns)
}

func TestU32B06cCommitSameTransaction(t *testing.T) {
	dir := u32Repo(t)
	check := func(source string) (int, []string, []string) {
		root := u32Query(t, dir, source, "--related-entry",
			"symbol:go:example.com/remaining:Review")
		wf, _ := root["workflow"].(map[string]any)
		c := u32Conn(wf, source, "Review")
		if c == nil {
			t.Fatalf("%s 缺少连接", source)
		}
		var conds, missing []string
		for _, v := range asAnySlice(c["success_conditions"]) {
			conds = append(conds, asString(v))
		}
		for _, v := range asAnySlice(c["missing_proofs"]) {
			if v == "transaction_commit" {
				missing = append(missing, "transaction_commit")
			}
		}
		return u32ConnCount(wf), conds, missing
	}
	// 同一事务 Commit 仍有 commit_success。
	n, conds, missing := check("SubmitTx")
	if n != 1 || !containsString(conds, "commit_success") || len(missing) != 0 {
		t.Fatalf("SubmitTx = %d/%v/%v", n, conds, missing)
	}
	// 另一事务 Commit / 忽略 Commit 错误 / 未检查 Commit：保留 transaction_commit。
	for _, source := range []string{"SubmitOtherTx", "SubmitTxIgnored"} {
		n, conds, missing := check(source)
		if n != 1 || containsString(conds, "commit_success") ||
			!containsString(missing, "transaction_commit") {
			t.Fatalf("%s = %d/%v/%v（want 1/[]/[transaction_commit]）", source, n, conds, missing)
		}
	}
}

func TestU32U04bSResourcePosition(t *testing.T) {
	dir := u32Repo(t)
	count := func(target string) int {
		root := u32Query(t, dir, "Submit", "--related-entry",
			"symbol:go:example.com/remaining:"+target)
		wf, _ := root["workflow"].(map[string]any)
		return u32ConnCount(wf)
	}
	// 未执行闭包 / 读取后才改写 → 读取点仍有效。
	for _, target := range []string{"ReviewUnusedReset", "ReviewAfterReset"} {
		if got := count(target); got != 1 {
			t.Fatalf("%s connections = %d（want 1）", target, got)
		}
	}
	// 读取前直接改写 / 已执行闭包改写 → 拒绝旧绑定。
	for _, target := range []string{"ReviewBeforeReset", "ReviewClosureExec"} {
		if got := count(target); got != 0 {
			t.Fatalf("%s connections = %d（want 0）", target, got)
		}
	}
}

func TestU32B09cFrontendIdentity(t *testing.T) {
	dir := u32Repo(t)
	root := u32Query(t, dir, "SubmitHTTP", "--related-entry",
		"symbol:go:example.com/remaining:ReviewHTTP",
		"--frontend-root", "identity-frontend")
	wf, _ := root["workflow"].(map[string]any)
	fr, _ := wf["frontend"].(map[string]any)
	actions, _ := fr["actions"].([]any)
	if len(actions) != 1 {
		t.Fatalf("actions = %d（want 1）", len(actions))
	}
	a, _ := actions[0].(map[string]any)
	if a["action"] != "click" || a["method"] != "POST" || a["path"] != "/requests" ||
		a["entry"] != "symbol:go:example.com/remaining:SubmitHTTP" {
		t.Fatalf("action = %v（want click POST /requests SubmitHTTP）", a)
	}
}

func TestU32B03cDAOModel(t *testing.T) {
	dir := u32Repo(t)
	root := u32Query(t, dir, "ORMUpdate", "--related-entry",
		"symbol:go:example.com/remaining:ReviewDAO")
	wf, _ := root["workflow"].(map[string]any)
	if got := u32ConnCount(wf); got != 1 {
		t.Fatalf("ORM Update→DAO connections = %d（want 1）", got)
	}
}

func TestU32B10cScenarioWorkflows(t *testing.T) {
	dir := u32Repo(t)
	root := u32Query(t, dir, "Entry", "--related-entry",
		"symbol:go:example.com/remaining:Review",
		"--combination-selector", "symbol:go:example.com/remaining:Supported")
	wf, _ := root["workflow"].(map[string]any)
	scens, _ := wf["scenarios"].([]any)
	if len(scens) != 2 {
		t.Fatalf("scenarios = %d", len(scens))
	}
	got := map[string][2]int{}
	for _, raw := range scens {
		sc, _ := raw.(map[string]any)
		params, _ := sc["parameters"].(map[string]any)
		sw, _ := sc["workflow"].(map[string]any)
		entries, _ := sw["entries"].([]any)
		conns, _ := sw["connections"].([]any)
		got[asString(params["region"])] = [2]int{len(entries), len(conns)}
	}
	if got["ID"] != [2]int{2, 1} || got["MY"] != [2]int{2, 1} {
		t.Fatalf("scenario workflows = %v（want ID/MY 2/1）", got)
	}
}

func TestU32B11cInvalidManifest(t *testing.T) {
	dir := u32Repo(t)
	baseDir := filepath.Dir(dir)
	// 使用独立输出目录：非法参数不得创建任何文件。
	outDir := filepath.Join(baseDir, "invalid-export")
	manifest := `{"version":1,"flows":[{"id":"review","repo":".","entry":"symbol:go:example.com/remaining:SubmitTx","relatedEntries":["symbol:go:example.com/remaining:Review"],"timeout":"invalid"}],"formats":["markdown"]}`
	mpath := filepath.Join(dir, "flows-invalid.json")
	writeFile(t, mpath, manifest)
	code, out := runCLIOut(t, "sequence-batch", "--manifest", mpath, "--out", outDir, "--json")
	if code != 1 {
		t.Fatalf("exit = %d（want 1）\n%s", code, out)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("非法参数不应输出 stdout: %q", out)
	}
	if _, err := os.Stat(outDir); !os.IsNotExist(err) {
		t.Fatalf("非法参数不应创建输出目录: %v", err)
	}
	// 其它非法参数同样在执行前拒绝。
	cases := []string{
		`{"version":1,"flows":[{"id":"review","repo":".","entry":"symbol:go:example.com/remaining:SubmitTx","depth":-1}],"formats":["markdown"]}`,
		`{"version":1,"flows":[{"id":"review","repo":".","entry":"symbol:go:example.com/remaining:SubmitTx","continueSegments":129}],"formats":["markdown"]}`,
		`{"version":1,"flows":[{"id":"review","repo":".","entry":"symbol:go:example.com/remaining:SubmitTx","scene":["bad"]}],"formats":["markdown"]}`,
		`{"version":1,"flows":[{"id":"review","repo":".","entry":"symbol:go:example.com/remaining:SubmitTx","combinationSelector":"not-canonical"}],"formats":["markdown"]}`,
		`{"version":1,"flows":[{"id":"review","repo":".","entry":"symbol:go:example.com/remaining:SubmitTx","runtimeData":"missing.json"}],"formats":["markdown"]}`,
	}
	for i, m := range cases {
		p := filepath.Join(dir, "flows-bad-"+strconv.Itoa(i)+".json")
		writeFile(t, p, m)
		od := filepath.Join(baseDir, "bad-"+strconv.Itoa(i))
		code, out := runCLIOut(t, "sequence-batch", "--manifest", p, "--out", od, "--json")
		if code == 0 || strings.TrimSpace(out) != "" {
			t.Fatalf("case %d 应执行前失败且 stdout 为空: code=%d out=%q", i, code, out)
		}
		if _, err := os.Stat(od); !os.IsNotExist(err) {
			t.Fatalf("case %d 不应创建输出目录", i)
		}
	}
	// 合法批量仍可导出（runtimeData 相对 manifest 解析）。
	rt := filepath.Join(dir, "runtime.json")
	writeFile(t, rt, `{"version":1,"environment":"test","region":"ID","captured_at":"2026-11-05T00:00:00Z","bindings":[{"symbol":"symbol:go:example.com/remaining:DB","database":"review"}],"databases":[{"id":"review","tables":[{"name":"nodes","scope":{"id":"n1"},"complete":true,"rows":[{"id":"n1","kind":"profile"}]}]}]}`)
	okManifest := `{"version":1,"flows":[{"id":"review","repo":".","entry":"symbol:go:example.com/remaining:SubmitTx","relatedEntries":["symbol:go:example.com/remaining:Review"],"runtimeData":"runtime.json"}],"formats":["markdown"]}`
	mp := filepath.Join(dir, "flows-ok.json")
	writeFile(t, mp, okManifest)
	od := filepath.Join(baseDir, "ok-export")
	code, out = runCLIOut(t, "sequence-batch", "--manifest", mp, "--out", od, "--json")
	if code != 0 {
		t.Fatalf("合法批量 exit=%d\n%s", code, out)
	}
	for _, name := range []string{"review.md", "index.md"} {
		if _, err := os.Stat(filepath.Join(od, name)); err != nil {
			t.Fatalf("缺少产物 %s", name)
		}
	}
}

func asAnySlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
