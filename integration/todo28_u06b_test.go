//go:build integration

package integration

// todo28 U06b：候选截断与续查边界（真实源码 → init → CLI）。
//
// 13 个接口实现 > 候选预算 → 出现 candidate_budget 节点且 candidate=true；
// depth=1 + 续查与 depth=12 同口径，候选后代不计确定执行。

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func u06bFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/receiver\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), `package receiver
type Many interface{Run()}
func ManyEntry(v Many){v.Run()}
func Hit(){}
`)
	var b strings.Builder
	b.WriteString("package receiver\n")
	for i := 0; i < 13; i++ {
		n := "M" + itoaInt(i)
		b.WriteString("type " + n + " struct{}\n")
		b.WriteString("func(" + n + ")Run(){Hit()}\n")
	}
	writeFile(t, filepath.Join(dir, "many.go"), b.String())
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

// TestU06bCandidateBudgetCandidateFlag 截断节点必须 candidate=true（深浅一致）。
func TestU06bCandidateBudgetCandidateFlag(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u06bFixture(t)
	for _, args := range [][2]string{{"12", "32"}, {"1", "32"}} {
		code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/receiver:ManyEntry",
			"--repo", dir, "--code", "--depth", args[0], "--continue-segments", args[1],
			"--evidence", "--json")
		if code != 0 {
			t.Fatalf("query exit=%d\n%s", code, out)
		}
		var root any
		if err := json.Unmarshal([]byte(out), &root); err != nil {
			t.Fatalf("json: %v", err)
		}
		var budgets []map[string]any
		var hitCount int
		var hitCertain int
		var walk func(v any, candidate bool)
		walk = func(v any, candidate bool) {
			switch n := v.(type) {
			case map[string]any:
				if cand, _ := n["candidate"].(bool); cand {
					candidate = true
				}
				if stop, ok := n["stop"].(map[string]any); ok && stop["code"] == "candidate_budget" {
					budgets = append(budgets, map[string]any{"candidate": cookie(n["candidate"])})
				}
				if n["Label"] == "Hit" {
					hitCount++
					if !candidate {
						hitCertain++
					}
				}
				for _, c := range n {
					walk(c, candidate)
				}
			case []any:
				for _, c := range n {
					walk(c, candidate)
				}
			}
		}
		walk(root, false)
		if len(budgets) == 0 {
			t.Fatalf("depth=%s 缺少 candidate_budget 节点", args[0])
		}
		for _, b := range budgets {
			if b["candidate"] != true {
				t.Fatalf("depth=%s candidate_budget 节点必须 candidate=true: %+v", args[0], b)
			}
		}
		if hitCertain != 0 {
			t.Fatalf("depth=%s 候选后代不得计确定执行（Hit=%d）", args[0], hitCertain)
		}
	}
}

// cookie 读取 JSON 布尔标记（缺失 → false）。
func cookie(v any) bool {
	b, _ := v.(bool)
	return b
}
