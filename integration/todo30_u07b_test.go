//go:build integration

package integration

// todo30 U07b：关联入口中同名参与者的独立身份——真实源码 → init → CLI。
// 参与者在渲染层按入口作用域隔离；JSON 原分析树（Actor/Nodes/Else）不被
// 渲染动作修改。

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

const u30AlphaSrc = `package alpha

type DB struct{}

func (*DB) Save() {}

func Run() {
	var db DB
	db.Save()
}
`

// u30AlphaBetaRepo 建 alpha/beta 两个包（入口短名相同、局部变量同名）。
func u30AlphaBetaRepo(t *testing.T) string {
	t.Helper()
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/flow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "alpha", "run.go"), u30AlphaSrc)
	writeFile(t, filepath.Join(dir, "beta", "run.go"), strings.Replace(u30AlphaSrc, "package alpha", "package beta", 1))
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

func TestU30U07bParticipantScopes(t *testing.T) {
	dir := u30AlphaBetaRepo(t)
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/flow/alpha:Run",
		"--related-entry", "symbol:go:example.com/flow/beta:Run",
		"--repo", dir, "--code", "--depth", "4", "--format", "mermaid")
	if code != 0 {
		t.Fatalf("mermaid exit=%d\n%s", code, out)
	}
	var got []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "  participant ") {
			got = append(got, line)
		}
	}
	want := []string{
		"  participant P0 as External entries",
		"  participant P1 as symbol:go:example.com/flow/alpha:Run",
		"  participant P2 as symbol:go:example.com/flow/alpha:Run.db<br/>alpha.DB",
		"  participant P3 as symbol:go:example.com/flow/beta:Run",
		"  participant P4 as symbol:go:example.com/flow/beta:Run.db<br/>beta.DB",
	}
	if len(got) != len(want) {
		t.Fatalf("participant 行数 = %d（want %d）:\n%s", len(got), len(want), out)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("participant[%d] = %q（want %q）\n%s", i, got[i], want[i], out)
		}
	}

	// JSON 原分析树不被渲染动作修改：Actor 仍是源码里的短名。
	code, js := runCLIOut(t, "query", "sequence", "symbol:go:example.com/flow/alpha:Run",
		"--related-entry", "symbol:go:example.com/flow/beta:Run",
		"--repo", dir, "--code", "--depth", "4", "--json")
	if code != 0 {
		t.Fatalf("json exit=%d\n%s", code, js)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(js), &root); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	wf, _ := root["workflow"].(map[string]any)
	entries, _ := wf["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("entries = %d", len(entries))
	}
	var actors []string
	var collect func(nodes []any)
	collect = func(nodes []any) {
		for _, raw := range nodes {
			n, _ := raw.(map[string]any)
			if a, ok := n["Actor"].(string); ok && a != "" {
				actors = append(actors, a)
			}
			sub, _ := n["Nodes"].([]any)
			collect(sub)
			els, _ := n["Else"].([]any)
			collect(els)
		}
	}
	for _, raw := range entries {
		e, _ := raw.(map[string]any)
		seq, _ := e["sequence"].(map[string]any)
		nodes, _ := seq["Nodes"].([]any)
		collect(nodes)
	}
	for _, a := range actors {
		if strings.Contains(a, "symbol:go:") {
			t.Fatalf("JSON 原树 Actor 不应被渲染改成作用域 ID: %q", a)
		}
	}

	// 默认单入口输出保持原参与者（不引入 External entries）。
	code, single := runCLIOut(t, "query", "sequence", "symbol:go:example.com/flow/alpha:Run",
		"--repo", dir, "--code", "--depth", "4", "--format", "mermaid")
	if code != 0 {
		t.Fatalf("single exit=%d", code)
	}
	if strings.Contains(single, "External entries") || !strings.Contains(single, "  participant P0 as Run\n") {
		t.Fatalf("单入口参与者被改变:\n%s", single)
	}
}
