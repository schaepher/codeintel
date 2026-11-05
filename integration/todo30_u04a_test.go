//go:build integration

package integration

// todo30 U04a：关联入口集合——真实源码 → init → CLI。
// 只验收入口集合（校验/去重/排序/共享预算/workflow.entries）；资源、记录键
// 与条件化连接仍属 U04b（本测试不把入口集合当请求顺序）。

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

const u30FlowSrc = `package flow
import("context";"database/sql")
var DB *sql.DB
func CreateNode(ctx context.Context,id,kind string){
 DB.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,kind)
}
func Submit(ctx context.Context,id string){CreateNode(ctx,id,"profile")}
func Complete(string){}
func FinishReview(ctx context.Context,id string){
 var kind string
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&kind);err!=nil{return}
 if kind=="profile"{CreateNode(ctx,id+"-service","service")}else{Complete(id)}
}
`

// u30FlowRepo 建 example.com/flow 仓库并初始化索引。
func u30FlowRepo(t *testing.T) string {
	t.Helper()
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/flow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), u30FlowSrc)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

func u30QueryWorkflow(t *testing.T, dir string, related ...string) map[string]any {
	t.Helper()
	args := []string{"query", "sequence", "symbol:go:example.com/flow:Submit",
		"--repo", dir, "--code", "--depth", "12", "--json"}
	for _, r := range related {
		args = append(args, "--related-entry", r)
	}
	code, out := runCLIOut(t, args...)
	if code != 0 {
		t.Fatalf("query exit=%d\n%s", code, out)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("JSON: %v\n%s", err, out)
	}
	wf, _ := root["workflow"].(map[string]any)
	if wf == nil {
		t.Fatalf("缺少 workflow 字段:\n%s", out)
	}
	return wf
}

func TestU30U04aRelatedEntries(t *testing.T) {
	dir := u30FlowRepo(t)
	wf := u30QueryWorkflow(t, dir, "symbol:go:example.com/flow:FinishReview")
	entries, _ := wf["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("entries = %d（want 2）", len(entries))
	}
	if wf["ordering"] != "unproven" {
		t.Fatalf("ordering = %v（want unproven）", wf["ordering"])
	}
	first, _ := entries[0].(map[string]any)
	second, _ := entries[1].(map[string]any)
	if first["symbol"] != "symbol:go:example.com/flow:Submit" ||
		second["symbol"] != "symbol:go:example.com/flow:FinishReview" {
		t.Fatalf("入口顺序/身份不符: %v / %v", first["symbol"], second["symbol"])
	}
	for _, e := range []map[string]any{first, second} {
		if e["repo"] != dir {
			t.Fatalf("entry.repo = %v（want %s）", e["repo"], dir)
		}
		if _, ok := e["sequence"].(map[string]any); !ok {
			t.Fatalf("entry 缺少 sequence 分析树: %v", e)
		}
	}

	// 重复入口（含根入口自身）只分析一次；根入口外按 canonical ID 排序。
	wf = u30QueryWorkflow(t, dir,
		"symbol:go:example.com/flow:FinishReview",
		"symbol:go:example.com/flow:Submit",
		"symbol:go:example.com/flow:Complete",
		"symbol:go:example.com/flow:FinishReview")
	entries, _ = wf["entries"].([]any)
	if len(entries) != 3 {
		t.Fatalf("去重后 entries = %d（want 3）", len(entries))
	}
	var got []string
	for _, raw := range entries {
		e, _ := raw.(map[string]any)
		got = append(got, e["symbol"].(string))
	}
	want := []string{"symbol:go:example.com/flow:Submit",
		"symbol:go:example.com/flow:Complete",
		"symbol:go:example.com/flow:FinishReview"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("入口排序 = %v（want %v）", got, want)
		}
	}
}

// TestU30U04aRejectsInvalidEntries：非 canonical/不存在/用错命令都在开库前失败。
func TestU30U04aRejectsInvalidEntries(t *testing.T) {
	dir := u30FlowRepo(t)
	cases := [][]string{
		{"query", "sequence", "symbol:go:example.com/flow:Submit", "--code", "--json",
			"--repo", dir, "--related-entry", "FinishReview"},
		{"query", "sequence", "symbol:go:example.com/flow:Submit", "--code", "--json",
			"--repo", dir, "--related-entry", "symbol:go:example.com/flow:NoSuch"},
		{"query", "sequence", "symbol:go:example.com/flow:Submit", "--code", "--json",
			"--repo", dir, "--related-entry="},
		{"query", "sequence", "symbol:go:example.com/flow:Submit", "--code", "--json",
			"--repo", dir, "--related-entry", "--evidence"},
		{"query", "symbol", "symbol:go:example.com/flow:Submit", "--json",
			"--repo", dir, "--related-entry", "symbol:go:example.com/flow:FinishReview"},
	}
	for i, args := range cases {
		code, out := runCLIOut(t, args...)
		if code == 0 {
			t.Fatalf("case %d 应非零退出:\n%s", i, out)
		}
		if strings.Contains(out, `"workflow"`) {
			t.Fatalf("case %d 失败时不得输出半份结果:\n%s", i, out)
		}
	}
}

// TestU30U04aSharesBudgetAndRenders：集合共享追加预算；文本/Mermaid 标注顺序未证明。
func TestU30U04aSharesBudgetAndRenders(t *testing.T) {
	dir := u30FlowRepo(t)
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/flow:Submit",
		"--related-entry", "symbol:go:example.com/flow:FinishReview",
		"--repo", dir, "--code", "--depth", "1", "--continue-segments", "1", "--json")
	if code != 0 {
		t.Fatalf("query exit=%d\n%s", code, out)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	wf, _ := root["workflow"].(map[string]any)
	cont, _ := wf["continuation"].(map[string]any)
	if cont == nil {
		t.Fatalf("workflow.continuation 缺失: %s", out)
	}
	expanded := int(cont["expanded"].(float64))
	if expanded > 1 {
		t.Fatalf("集合共享预算 expanded=%d（want ≤1）", expanded)
	}
	if int(cont["segments_budget"].(float64)) != 1 {
		t.Fatalf("segments_budget = %v（want 1）", cont["segments_budget"])
	}

	code, mermaid := runCLIOut(t, "query", "sequence", "symbol:go:example.com/flow:Submit",
		"--related-entry", "symbol:go:example.com/flow:FinishReview",
		"--repo", dir, "--code", "--depth", "12", "--format", "mermaid")
	if code != 0 {
		t.Fatalf("mermaid exit=%d\n%s", code, mermaid)
	}
	if !strings.Contains(mermaid, "entry order unproven") ||
		!strings.Contains(mermaid, "selected root instance-1: Submit") ||
		!strings.Contains(mermaid, "conditional external request instance-2: FinishReview") {
		t.Fatalf("Mermaid 未展示全部入口/顺序未证明:\n%s", mermaid)
	}

	code, text := runCLIOut(t, "query", "sequence", "symbol:go:example.com/flow:Submit",
		"--related-entry", "symbol:go:example.com/flow:FinishReview",
		"--repo", dir, "--code", "--depth", "12")
	if code != 0 {
		t.Fatalf("text exit=%d\n%s", code, text)
	}
	// 有请求实例时走实例视图（U04g）；仍展示全部入口且标注顺序未证明。
	for _, want := range []string{"== selected root instance-1: Submit ==",
		"== conditional external request instance-2: FinishReview ==",
		"entry order unproven"} {
		if !strings.Contains(text, want) {
			t.Fatalf("文本缺少 %q:\n%s", want, text)
		}
	}

	// 默认单入口输出保持原格式（不得出现 workflow / 集合标题）。
	code, single := runCLIOut(t, "query", "sequence", "symbol:go:example.com/flow:Submit",
		"--repo", dir, "--code", "--depth", "12", "--json")
	if code != 0 {
		t.Fatalf("single exit=%d", code)
	}
	if strings.Contains(single, `"workflow"`) {
		t.Fatalf("单入口 JSON 不应出现 workflow:\n%s", single)
	}
	code, singleText := runCLIOut(t, "query", "sequence", "symbol:go:example.com/flow:Submit",
		"--repo", dir, "--code", "--depth", "12")
	if code != 0 || !strings.Contains(singleText, "== Submit 代码级时序 ==\n") {
		t.Fatalf("单入口文本格式被改变:\n%s", singleText)
	}
}
