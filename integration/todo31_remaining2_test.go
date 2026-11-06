//go:build integration

package integration

// todo31：B05b/B06b/B09b/B10b/B11b（源码 → init → CLI）。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestU31B05bORMGeneratedKey(t *testing.T) {
	dir := u31Repo(t)
	origin := func(entry string) map[string]any {
		root := u31Query(t, dir, entry)
		var o map[string]any
		var walk func(nodes []any)
		walk = func(nodes []any) {
			for _, raw := range nodes {
				n, _ := raw.(map[string]any)
				ev, _ := n["evidence"].(map[string]any)
				st, _ := ev["storage"].(map[string]any)
				if st["table"] == "audits" {
					writes, _ := st["writes"].([]any)
					if len(writes) > 0 {
						w, _ := writes[0].(map[string]any)
						v, _ := w["value"].(map[string]any)
						o, _ = v["origin"].(map[string]any)
					}
				}
				if sub, ok := n["Nodes"].([]any); ok {
					walk(sub)
				}
				if sub, ok := n["Else"].([]any); ok {
					walk(sub)
				}
			}
		}
		nodes, _ := root["Nodes"].([]any)
		walk(nodes)
		return o
	}
	o := origin("ORMGenerated")
	if asString(o["kind"]) != "generated_key" || o["generated_key"] == nil {
		t.Fatalf("ORM 自动主键回填来源 = %v（want generated_key）", o)
	}
	gk, _ := o["generated_key"].(map[string]any)
	if asString(gk["backfill_field"]) != "ID" {
		t.Fatalf("回填字段 = %v（want ID）", gk["backfill_field"])
	}
	// 反例：显式主键 → 不借用回填来源。
	if o := origin("ORMGeneratedExplicit"); asString(o["kind"]) == "generated_key" {
		t.Fatalf("显式主键不应借用 generated_key")
	}
}

func TestU31B06bCommitSuccess(t *testing.T) {
	dir := u31Repo(t)
	root := u31Query(t, dir, "SubmitTx", "--related-entry",
		"symbol:go:example.com/remaining:Review")
	wf, _ := root["workflow"].(map[string]any)
	c := u31Connection(wf, "SubmitTx", "Review")
	if c == nil {
		t.Fatalf("缺少 SubmitTx→Review 连接")
	}
	conds, _ := c["success_conditions"].([]any)
	missing, _ := c["missing_proofs"].([]any)
	hasCommit := false
	for _, v := range conds {
		if v == "commit_success" {
			hasCommit = true
		}
	}
	for _, v := range missing {
		if v == "transaction_commit" {
			t.Fatalf("已证明 Commit 检查仍保留 transaction_commit: %v", missing)
		}
	}
	if !hasCommit {
		t.Fatalf("success_conditions 缺少 commit_success: %v", conds)
	}
	// 反例：忽略 Commit 错误 → 不携带 commit_success。
	root = u31Query(t, dir, "SubmitTxUnchecked", "--related-entry",
		"symbol:go:example.com/remaining:Review")
	wf, _ = root["workflow"].(map[string]any)
	c = u31Connection(wf, "SubmitTxUnchecked", "Review")
	if c != nil {
		if cs, _ := c["success_conditions"].([]any); len(cs) > 0 {
			t.Fatalf("未检查 Commit 错误不应携带成功约束: %v", cs)
		}
	}
}

func TestU31B09bFrontendWrapper(t *testing.T) {
	dir := u31Repo(t)
	root := u31Query(t, dir, "SubmitHTTP", "--related-entry",
		"symbol:go:example.com/remaining:ReviewHTTP", "--frontend-root", "frontend")
	wf, _ := root["workflow"].(map[string]any)
	fr, _ := wf["frontend"].(map[string]any)
	actions, _ := fr["actions"].([]any)
	type row struct{ A, M, P, E string }
	var got []row
	for _, raw := range actions {
		x, _ := raw.(map[string]any)
		got = append(got, row{asString(x["action"]), asString(x["method"]),
			asString(x["path"]), asString(x["entry"])})
	}
	want := []row{
		{"onFinishClick", "POST", "/reviews/finish", "symbol:go:example.com/remaining:ReviewHTTP"},
		{"onSubmitClick", "POST", "/requests", "symbol:go:example.com/remaining:SubmitHTTP"},
	}
	if len(got) != len(want) {
		t.Fatalf("actions = %v（want %v）", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("action[%d] = %+v（want %+v）", i, got[i], want[i])
		}
	}
}

func TestU31B10bScenarios(t *testing.T) {
	dir := u31Repo(t)
	root := u31Query(t, dir, "Entry", "--related-entry",
		"symbol:go:example.com/remaining:Review", "--combination-selector",
		"symbol:go:example.com/remaining:Supported")
	wf, _ := root["workflow"].(map[string]any)
	cov, _ := wf["coverage"].(map[string]any)
	if cov["supported"] != float64(2) || cov["analyzed"] != float64(2) {
		t.Fatalf("coverage = %v（want supported/analyzed=2）", cov)
	}
	scens, _ := wf["scenarios"].([]any)
	if len(scens) != 2 {
		t.Fatalf("scenarios = %d（want 2）", len(scens))
	}
	seen := map[string]string{}
	for _, raw := range scens {
		sc, _ := raw.(map[string]any)
		params, _ := sc["parameters"].(map[string]any)
		region := asString(params["region"])
		seq, _ := sc["sequence"].(map[string]any)
		status := ""
		var walk func(nodes []any)
		walk = func(nodes []any) {
			for _, nr := range nodes {
				n, _ := nr.(map[string]any)
				if n["Kind"] == "branch" && n["Label"] == `region=="ID"` {
					scene, _ := n["scene"].(map[string]any)
					status = asString(scene["status"])
				}
				if sub, ok := n["Nodes"].([]any); ok {
					walk(sub)
				}
				if sub, ok := n["Else"].([]any); ok {
					walk(sub)
				}
			}
		}
		nodes, _ := seq["Nodes"].([]any)
		walk(nodes)
		seen[region] = status
	}
	if seen["ID"] != "taken" || seen["MY"] != "excluded" {
		t.Fatalf("region 场景判定 = %v（want ID=taken, MY=excluded）", seen)
	}
}

func TestU31B11bBatchRuntimeAndScenarios(t *testing.T) {
	dir := u31Repo(t)
	manifest := `{"version":1,"flows":[{"id":"review","repo":".","entry":"symbol:go:example.com/remaining:SubmitTx","relatedEntries":["symbol:go:example.com/remaining:Review"],"runtimeData":"runtime.json"}],"formats":["markdown"]}`
	mpath := filepath.Join(dir, "flows.json")
	writeFile(t, mpath, manifest)
	outDir := filepath.Join(dir, "exported")
	code, out := runCLIOut(t, "sequence-batch", "--manifest", mpath, "--out", outDir, "--json")
	if code != 0 {
		t.Fatalf("batch exit=%d\n%s", code, out)
	}
	var sum struct {
		Total    int      `json:"total"`
		Exported int      `json:"exported"`
		Failed   int      `json:"failed"`
		Files    []string `json:"files"`
	}
	if err := json.Unmarshal([]byte(out), &sum); err != nil {
		t.Fatalf("summary: %v", err)
	}
	if sum.Total != 1 || sum.Exported != 1 || sum.Failed != 0 ||
		strings.Join(sum.Files, ",") != "review.md,index.md" {
		t.Fatalf("summary = %+v", sum)
	}
	md, err := os.ReadFile(filepath.Join(outDir, "review.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(md), "sha256=") || !strings.Contains(string(md), "replay:") {
		t.Fatalf("Markdown 缺少重放身份: %s", md)
	}
	// 组合选择器：逐场景导出（不复制同一张图）。
	manifest2 := `{"version":1,"flows":[{"id":"entry","repo":".","entry":"symbol:go:example.com/remaining:Entry","relatedEntries":["symbol:go:example.com/remaining:Review"],"combinationSelector":"symbol:go:example.com/remaining:Supported"}],"formats":["markdown"]}`
	mpath2 := filepath.Join(dir, "flows-sc.json")
	writeFile(t, mpath2, manifest2)
	out2 := filepath.Join(dir, "exp-sc")
	code, out = runCLIOut(t, "sequence-batch", "--manifest", mpath2, "--out", out2, "--json")
	if code != 0 {
		t.Fatalf("batch(scenarios) exit=%d\n%s", code, out)
	}
	if err := json.Unmarshal([]byte(out), &sum); err != nil {
		t.Fatalf("summary: %v", err)
	}
	joined := strings.Join(sum.Files, ",")
	if !strings.Contains(joined, "entry-scenario-1.md") || !strings.Contains(joined, "entry-scenario-2.md") {
		t.Fatalf("组合场景未逐项导出: %v", sum.Files)
	}
}
