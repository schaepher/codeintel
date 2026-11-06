//go:build integration

package integration

// todo33：B06d/U04bT/B09d/B03d/B10d/B11d（源码 → init → CLI）。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func u33Query(t *testing.T, dir, entry string, extra ...string) map[string]any {
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

func u33Conn(wf map[string]any, from, to string) map[string]any {
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

func TestU33B06dConditionalCommit(t *testing.T) {
	dir := u33Repo(t)
	check := func(source string) (int, []string, []string) {
		root := u33Query(t, dir, source, "--related-entry",
			"symbol:go:example.com/remaining:Review")
		wf, _ := root["workflow"].(map[string]any)
		c := u33Conn(wf, source, "Review")
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
		return u33ConnCount(wf), conds, missing
	}
	n, conds, missing := check("SubmitTx")
	if n != 1 || !containsString(conds, "commit_success") || len(missing) != 0 {
		t.Fatalf("SubmitTx = %d/%v/%v", n, conds, missing)
	}
	n, conds, missing = check("SubmitConditionalTx")
	if n != 1 || containsString(conds, "commit_success") ||
		!containsString(missing, "transaction_commit") {
		t.Fatalf("SubmitConditionalTx = %d/%v/%v（want 1/[]/[transaction_commit]）", n, conds, missing)
	}
}

func TestU33U04bTResourceIdentity(t *testing.T) {
	dir := u33Repo(t)
	count := func(entry, target string) int {
		root := u33Query(t, dir, entry, "--related-entry",
			"symbol:go:example.com/remaining:"+target)
		wf, _ := root["workflow"].(map[string]any)
		return u33ConnCount(wf)
	}
	for _, target := range []string{"ReviewShadow", "ReviewExcludedElse"} {
		if got := count("Submit", target); got != 1 {
			t.Fatalf("%s connections = %d（want 1）", target, got)
		}
	}
	// 子目录包中 DB=nil 在读取之前 → 拒绝旧绑定。
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/remaining/worker:Submit",
		"--related-entry", "symbol:go:example.com/remaining/worker:ReviewBeforeReset",
		"--repo", dir, "--code", "--depth", "12", "--json")
	if code != 0 {
		t.Fatalf("worker query exit=%d\n%s", code, out)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("worker JSON: %v", err)
	}
	wf, _ := root["workflow"].(map[string]any)
	if got := u33ConnCount(wf); got != 0 {
		t.Fatalf("worker ReviewBeforeReset connections = %d（want 0）", got)
	}
}

func TestU33B09dFrontendAliasAndBody(t *testing.T) {
	dir := u33Repo(t)
	root := u33Query(t, dir, "SubmitHTTP", "--related-entry",
		"symbol:go:example.com/remaining:ReviewHTTP",
		"--frontend-root", "alias-frontend")
	wf, _ := root["workflow"].(map[string]any)
	fr, _ := wf["frontend"].(map[string]any)
	actions, _ := fr["actions"].([]any)
	got := map[string]map[string]any{}
	for _, raw := range actions {
		a, _ := raw.(map[string]any)
		got[asString(a["action"])] = a
	}
	for _, name := range []string{"click", "submitClick"} {
		a := got[name]
		if a == nil || a["method"] != "POST" || a["path"] != "/requests" ||
			a["entry"] != "symbol:go:example.com/remaining:SubmitHTTP" {
			t.Fatalf("%s action = %v", name, a)
		}
	}
	body, _ := got["click"]["request_body"].(map[string]any)
	id, _ := body["id"].(map[string]any)
	if id["kind"] != "parameter" || id["parameter"] != float64(0) {
		t.Fatalf("click request_body = %v（want id.parameter=0）", body)
	}
}

func TestU33B03dFindElementColumn(t *testing.T) {
	dir := u33Repo(t)
	root := u33Query(t, dir, "ORMUpdate", "--related-entry",
		"symbol:go:example.com/remaining:ReviewDAO")
	wf, _ := root["workflow"].(map[string]any)
	col := ""
	entries, _ := wf["entries"].([]any)
	for _, raw := range entries {
		e, _ := raw.(map[string]any)
		if !strings.HasSuffix(asString(e["symbol"]), "ReviewDAO") {
			continue
		}
		seq, _ := e["sequence"].(map[string]any)
		nodes, _ := seq["Nodes"].([]any)
		var walk func(nodes []any)
		walk = func(nodes []any) {
			for _, nr := range nodes {
				n, _ := nr.(map[string]any)
				if n["Kind"] == "branch" && n["Label"] == `r.Kind=="profile"` {
					c, _ := n["condition"].(map[string]any)
					l, _ := c["left"].(map[string]any)
					rc, _ := l["read_column"].(map[string]any)
					col = asString(rc["column"])
				}
				if sub, ok := n["Nodes"].([]any); ok {
					walk(sub)
				}
				if sub, ok := n["Else"].([]any); ok {
					walk(sub)
				}
			}
		}
		walk(nodes)
	}
	if col != "kind" {
		t.Fatalf("ReviewDAO r.Kind read_column = %q（want kind）", col)
	}
}

func TestU33B10dBatchScenarioWorkflow(t *testing.T) {
	dir := u33Repo(t)
	manifest := `{"version":1,"flows":[{"id":"review","repo":".","entry":"symbol:go:example.com/remaining:Entry","relatedEntries":["symbol:go:example.com/remaining:Review"],"combinationSelector":"symbol:go:example.com/remaining:Supported"}],"formats":["markdown","svg"]}`
	mpath := filepath.Join(dir, "flows.json")
	writeFile(t, mpath, manifest)
	outDir := filepath.Join(dir, "exported")
	code, out := runCLIOut(t, "sequence-batch", "--manifest", mpath, "--out", outDir, "--json")
	if code != 0 {
		t.Fatalf("batch exit=%d\n%s", code, out)
	}
	files, err := filepath.Glob(filepath.Join(outDir, "*-scenario-*.md"))
	if err != nil || len(files) != 2 {
		t.Fatalf("scenario md files = %v（want 2）", files)
	}
	for _, p := range files {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		s := string(data)
		if !strings.Contains(s, "Review") || !strings.Contains(s, "```plantuml") {
			t.Fatalf("%s 缺少审核流程或 PlantUML 图块", p)
		}
		svg := strings.TrimSuffix(p, ".md") + ".svg"
		if st, err := os.Stat(svg); err != nil || st.Size() == 0 {
			t.Fatalf("%s 缺少实际 SVG 渲染产物", svg)
		}
	}
}

func TestU33B11dTimeoutFullParse(t *testing.T) {
	dir := u33Repo(t)
	base := filepath.Dir(dir)
	cases := []string{"3600oops", "0", "-5", "999999999999999999999999"}
	for i, raw := range cases {
		manifest := `{"version":1,"flows":[{"id":"review","repo":".","entry":"symbol:go:example.com/remaining:SubmitTx","relatedEntries":["symbol:go:example.com/remaining:Review"],"timeout":"` + raw + `"}],"formats":["markdown"]}`
		mp := filepath.Join(dir, "flows-bad-"+strconv.Itoa(i)+".json")
		writeFile(t, mp, manifest)
		od := filepath.Join(base, "bad-timeout-"+strconv.Itoa(i))
		code, out := runCLIOut(t, "sequence-batch", "--manifest", mp, "--out", od, "--json")
		if code != 1 || strings.TrimSpace(out) != "" {
			t.Fatalf("timeout %q: code=%d out=%q（want 1/空）", raw, code, out)
		}
		if _, err := os.Stat(od); !os.IsNotExist(err) {
			t.Fatalf("timeout %q 不应创建输出目录", raw)
		}
	}
	// 合法单位仍可解析。
	manifest := `{"version":1,"flows":[{"id":"review","repo":".","entry":"symbol:go:example.com/remaining:SubmitTx","relatedEntries":["symbol:go:example.com/remaining:Review"],"timeout":"1h"}],"formats":["markdown"]}`
	mp := filepath.Join(dir, "flows-ok.json")
	writeFile(t, mp, manifest)
	od := filepath.Join(base, "ok-timeout")
	if code, out := runCLIOut(t, "sequence-batch", "--manifest", mp, "--out", od, "--json"); code != 0 {
		t.Fatalf("合法 timeout 失败: %d\n%s", code, out)
	}
	// 单流程同样拒绝非法后缀（开库前）。
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/remaining:SubmitTx",
		"--repo", dir, "--code", "--depth", "12", "--timeout", "3600oops", "--json")
	if code == 0 || strings.TrimSpace(out) != "" {
		t.Fatalf("单流程非法 timeout 应失败且 stdout 为空: code=%d out=%q", code, out)
	}
}

func u33ConnCount(wf map[string]any) int {
	conns, _ := wf["connections"].([]any)
	return len(conns)
}
