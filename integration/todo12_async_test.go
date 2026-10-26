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

// 调度注册（名称含 schedule → 入口注册证据；Advance 本身无 cron 关键词）
func ScheduleAdvance() {
	Advance(&review.Job{RecordID: "pending"})
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

	// todo12-6：入口注册证据 + 跨入口身份链
	var jobsNode *action.AsyncNode
	for i := range res.Nodes {
		if strings.HasPrefix(res.Nodes[i].ID, "symbol:go:example.com/app/jobs:Advance") {
			jobsNode = &res.Nodes[i]
		}
	}
	if jobsNode == nil {
		t.Fatalf("定时入口缺失: %+v", res.Nodes)
	}
	if jobsNode.Role != action.AsyncRoleCron || !strings.HasPrefix(jobsNode.RoleEvidence, "registration:") {
		t.Errorf("调度注册证据应判为 cron: %+v", jobsNode)
	}
	var chain *action.AsyncChain
	for i := range res.Chains {
		if strings.Contains(res.Chains[i].Field, "RecordID") {
			chain = &res.Chains[i]
		}
	}
	if chain == nil {
		t.Fatalf("应有 RecordID 身份链: %+v", res.Chains)
	}
	kinds := map[string]action.AsyncHop{}
	for _, h := range chain.Hops {
		kinds[h.Kind] = h
	}
	if w, ok := kinds["write"]; !ok || w.File == "" || w.Line == 0 {
		t.Errorf("写跳应带文件/行: %+v", chain.Hops)
	}
	if _, ok := kinds["persistence_or_queue"]; !ok {
		t.Errorf("未建模边界应显式列出: %+v", chain.Hops)
	}
	if r, ok := kinds["read"]; !ok || r.Line == 0 {
		t.Errorf("读跳应带行号: %+v", chain.Hops)
	}
	if f, ok := kinds["filter"]; !ok || !strings.Contains(f.Detail, "同一条记录") {
		t.Errorf("过滤条件应显式说明无法证明同一条记录: %+v", chain.Hops)
	}
	if chain.Verdict != action.SeqVerdictUnverified || !res.IdentityPartial {
		t.Errorf("边界未建模应 unverified + identity_partial: %+v", res)
	}
	boundaryDiag := false
	for _, d := range res.Diagnostics {
		if d.Code == "persistence_boundary_unverified" {
			boundaryDiag = true
		}
	}
	if !boundaryDiag {
		t.Errorf("应独立诊断持久化边界: %+v", res.Diagnostics)
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
