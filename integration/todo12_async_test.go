//go:build integration

package integration

// todo12-5 A1/A2：真实多包模块 → 生产索引 → CLI async。
//   - 审核入口的 nodes/links 只含与本 seed 有字段/调用关系的函数；
//     无关模块不得出现（不是只断言节点数变少）
//   - 无关系时输出 seed + 明确诊断，不填充无关关系

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
)

func TestTodo12AsyncScopeRealIndex(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/app\n\ngo 1.21\n")
	write("review/review.go", `package review

type Job struct{ RecordID string }
type Review struct{ State string }

type ReviewHandler struct{}

func (h *ReviewHandler) AfterReview(job *Job, requestID string) {
	job.RecordID = requestID
	r := &Review{State: "reviewing"}
	r.State = "approved"
}
`)
	write("jobs/jobs.go", `package jobs

import "example.com/app/review"

func Advance(job *review.Job) {
	if job.RecordID != "" {
		job.RecordID = "advanced"
	}
}
`)
	write("unrelated/unrelated.go", `package unrelated

type Unrelated struct{ Cache string }

func (u *Unrelated) Store() { u.Cache = "cached"; u.Read() }
func (u *Unrelated) Read() string { return u.Cache }
`)
	write("lonely/lonely.go", `package lonely

type Only struct{ Unique string }

func (o *Only) Run() { o.Unique = "x" }
`)
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}

	query := func(seed string) *action.AsyncLifecycleResult {
		t.Helper()
		code, out := runCLIOut(t, "query", "async", seed, "--repo", dir, "--json")
		if code != 0 {
			t.Fatalf("async exit = %d; %s", code, out)
		}
		var res action.AsyncLifecycleResult
		if err := json.Unmarshal([]byte(out), &res); err != nil {
			t.Fatalf("async JSON: %v\n%s", err, out)
		}
		return &res
	}

	seed := "symbol:go:example.com/app/review:(ReviewHandler).AfterReview"
	res := query(seed)
	ids := map[string]bool{}
	for _, n := range res.Nodes {
		ids[n.ID] = true
		if strings.Contains(n.ID, "unrelated") || strings.Contains(n.ID, "lonely") {
			t.Errorf("无关模块不得进入 nodes: %+v", n)
		}
	}
	if !ids[seed] {
		t.Errorf("seed 必须保留: %+v", res.Nodes)
	}
	hasJobs := false
	for id := range ids {
		if strings.Contains(id, "jobs") && strings.Contains(id, "Advance") {
			hasJobs = true
		}
	}
	if !hasJobs {
		t.Errorf("共享申请身份的定时入口应进入节点: %+v", ids)
	}
	for _, l := range res.Links {
		if !ids[l.From] || !ids[l.To] {
			t.Errorf("关系端点必须来自同一节点集合: %+v", l)
		}
		if strings.Contains(l.From, "unrelated") || strings.Contains(l.To, "unrelated") {
			t.Errorf("无关模块不得进入关系: %+v", l)
		}
	}

	// 无关系：lonely.Only.Run 的 Unique 字段无其他使用者
	lonely := query("symbol:go:example.com/app/lonely:(Only).Run")
	if len(lonely.Nodes) != 1 || lonely.Nodes[0].ID != "symbol:go:example.com/app/lonely:(Only).Run" {
		t.Errorf("无关系时 nodes 应只有 seed: %+v", lonely.Nodes)
	}
	if len(lonely.Links) != 0 {
		t.Errorf("无关系时不得填充 links: %+v", lonely.Links)
	}
	found := false
	for _, d := range lonely.Diagnostics {
		if d.Code == "no_related_links" {
			found = true
		}
	}
	if !found {
		t.Errorf("应明确说明未发现关联: %+v", lonely.Diagnostics)
	}
}
