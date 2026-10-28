//go:build integration

package integration

// todo25 §12：query async 候选预算按关系强度分配（真实 init → CLI）。

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
)

const todo25AsyncFlow = `package flow

type Flow struct{ Id string }

func Publish(f *Flow)     { f.Id = "record" }
func ZConsume(f *Flow) string { return f.Id }
func ZCaller(f *Flow)     { Publish(f) }
`

func todo25AsyncRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/boundary\n\ngo 1.26.2\n")
	var b strings.Builder
	b.WriteString(todo25AsyncFlow)
	for i := 0; i < 210; i++ {
		fmt.Fprintf(&b, "type Cache%03d struct{ Id string }\n", i)
		fmt.Fprintf(&b, "func A%03d(c *Cache%03d) string { return c.Id }\n", i, i)
	}
	writeFile(t, filepath.Join(dir, "flow.go"), b.String())
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "init")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit=%d", code)
	}
	return dir, "symbol:go:example.com/boundary:Publish"
}

func TestTodo25AsyncCandidateBudgetPriority(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir, seed := todo25AsyncRepo(t)
	code, out := runCLIOut(t, "query", "async", seed, "--repo", dir, "--json")
	if code != 0 {
		t.Fatalf("async exit=%d: %s", code, out)
	}
	var res action.AsyncLifecycleResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("async JSON: %v", err)
	}
	if res.CandidateTotal < 212 || res.CandidateBudget != 200 || !res.Partial {
		t.Errorf("预算契约不符: total=%d budget=%d partial=%v",
			res.CandidateTotal, res.CandidateBudget, res.Partial)
	}
	if len(res.Nodes) != 201 {
		t.Errorf("nodes=%d; want 201（seed + 预算内 200）", len(res.Nodes))
	}
	got := map[string]action.AsyncNode{}
	for _, n := range res.Nodes {
		got[n.ID] = n
	}
	zcall, ok := got["symbol:go:example.com/boundary:ZCaller"]
	if !ok || zcall.CandidateReason != "direct_call" {
		t.Errorf("ZCaller 应因直接调用入选: %+v", zcall)
	}
	zcons, ok := got["symbol:go:example.com/boundary:ZConsume"]
	if !ok || zcons.CandidateReason != "field_identity" {
		t.Errorf("ZConsume 应因字段身份入选: %+v", zcons)
	}
	for _, n := range res.Nodes {
		if strings.HasPrefix(n.Name, "A") && n.CandidateReason != "id_name" {
			t.Errorf("弱候选选入依据应为 id_name: %+v", n)
			break
		}
	}
	// links 端点都在 nodes 内（同一预算集合）。
	for _, l := range res.Links {
		if _, ok := got[l.From]; !ok {
			t.Errorf("link from 不在 nodes: %s", l.From)
		}
		if _, ok := got[l.To]; !ok {
			t.Errorf("link to 不在 nodes: %s", l.To)
		}
	}
	// 重复查询选择集合一致。
	code2, out2 := runCLIOut(t, "query", "async", seed, "--repo", dir, "--json")
	if code2 != 0 {
		t.Fatalf("async repeat exit=%d", code2)
	}
	var res2 action.AsyncLifecycleResult
	if err := json.Unmarshal([]byte(out2), &res2); err != nil {
		t.Fatal(err)
	}
	var ids1, ids2 []string
	for _, n := range res.Nodes {
		ids1 = append(ids1, n.ID)
	}
	for _, n := range res2.Nodes {
		ids2 = append(ids2, n.ID)
	}
	if strings.Join(ids1, ",") != strings.Join(ids2, ",") {
		t.Errorf("重复查询选择集合不一致")
	}
}
