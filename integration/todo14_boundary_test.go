//go:build integration

package integration

// todo14-6：持久化/消息边界的同记录身份闭环（生产索引 → CLI）。
//   - 同主题 + 同记录键字面量 → 逐跳 corroborated、identity_partial=false
//   - 键不一致 → mismatch 诊断，不升级
//   - 互斥分支不同键 → ambiguous，不串成一次生命周期

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
)

const todo14BoundaryHeader = `package flow

type Job struct{ RecordID string }

type Producer struct{}
type Consumer struct{}

func (p *Producer) Send(topic string, key string, v any) {}
func (c *Consumer) Receive(topic string) (string, Job) { return "", Job{} }

var producer = &Producer{}
var consumer = &Consumer{}
`

const todo14BoundaryProof = todo14BoundaryHeader + `
func Publish() *Job {
	job := &Job{}
	job.RecordID = "JOB-42"
	producer.Send("jobs.created", job.RecordID, job)
	return job
}

func Consume() Job {
	key, job := consumer.Receive("jobs.created")
	if key == "JOB-42" {
		_ = job.RecordID
	}
	return job
}
`

const todo14BoundaryBranched = todo14BoundaryHeader + `
func PublishBranched(flag bool) *Job {
	job := &Job{}
	if flag {
		job.RecordID = "JOB-A"
	} else {
		job.RecordID = "JOB-B"
	}
	producer.Send("jobs.created", job.RecordID, job)
	return job
}

func ConsumeBranched() Job {
	key, job := consumer.Receive("jobs.created")
	if key == "JOB-A" {
		_ = job.RecordID
	}
	return job
}
`

func buildBoundaryRepo(t *testing.T, src string) string {
	t.Helper()
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/boundary\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "flow.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

func queryBoundaryAsync(t *testing.T, dir, seed string) action.AsyncLifecycleResult {
	t.Helper()
	code, out := runCLIOut(t, "query", "async", "symbol:go:example.com/boundary:"+seed,
		"--repo", dir, "--json")
	if code != 0 {
		t.Fatalf("async %s exit=%d; %s", seed, code, out)
	}
	var res action.AsyncLifecycleResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("async JSON: %v\n%s", err, out)
	}
	return res
}

func recordChain(res action.AsyncLifecycleResult) *action.AsyncChain {
	for i := range res.Chains {
		if strings.Contains(res.Chains[i].Field, "RecordID") {
			return &res.Chains[i]
		}
	}
	return nil
}

func TestTodo14AsyncBoundaryIdentityRealIndex(t *testing.T) {
	dir := buildBoundaryRepo(t, todo14BoundaryProof)
	res := queryBoundaryAsync(t, dir, "Publish")
	chain := recordChain(res)
	if chain == nil {
		t.Fatalf("应有 RecordID 身份链: %+v", res.Chains)
	}
	if chain.Verdict != action.SeqVerdictCorroborated || res.IdentityPartial {
		t.Errorf("同键消息边界应完整证明: chain=%+v partial=%v diags=%+v", chain, res.IdentityPartial, res.Diagnostics)
	}
	if chain.Boundary == nil || chain.Boundary.Topic != "jobs.created" || chain.Boundary.Key != "JOB-42" ||
		chain.Boundary.ProduceLine == 0 || chain.Boundary.FilterLine == 0 {
		t.Errorf("边界证据应含主题/键/位置: %+v", chain.Boundary)
	}
	kinds := map[string]action.AsyncHop{}
	for _, h := range chain.Hops {
		kinds[h.Kind] = h
	}
	for _, want := range []string{"write", "persistence_or_queue", "read", "filter"} {
		h, ok := kinds[want]
		if !ok || h.File == "" || h.Line == 0 || h.Verdict != action.SeqVerdictCorroborated {
			t.Errorf("跳 %s 应 corroborated + 文件/行: %+v", want, h)
		}
	}
	if !strings.Contains(kinds["filter"].Detail, "JOB-42") {
		t.Errorf("过滤跳应说明同键: %+v", kinds["filter"])
	}
	for _, d := range res.Diagnostics {
		if strings.HasPrefix(d.Code, "boundary_") || d.Code == "persistence_boundary_unverified" {
			t.Errorf("已证明时不得再报边界问题: %+v", res.Diagnostics)
		}
	}
}

func TestTodo14AsyncBoundaryBranchedAmbiguous(t *testing.T) {
	dir := buildBoundaryRepo(t, todo14BoundaryBranched)
	res := queryBoundaryAsync(t, dir, "PublishBranched")
	chain := recordChain(res)
	if chain == nil {
		t.Fatalf("分支样例应有身份链: %+v", res.Chains)
	}
	if chain.Verdict == action.SeqVerdictCorroborated || !res.IdentityPartial {
		t.Errorf("互斥分支不同键不得升级: %+v partial=%v", chain, res.IdentityPartial)
	}
	found := false
	for _, d := range res.Diagnostics {
		if d.Code == "boundary_key_ambiguous" && strings.Contains(d.Detail, "JOB-A") &&
			strings.Contains(d.Detail, "JOB-B") {
			found = true
		}
	}
	if !found {
		t.Errorf("应报告互斥键歧义: %+v", res.Diagnostics)
	}
}

func TestTodo14AsyncBoundaryMismatchRealIndex(t *testing.T) {
	src := strings.Replace(todo14BoundaryProof, `if key == "JOB-42"`, `if key == "JOB-99"`, 1)
	dir := buildBoundaryRepo(t, src)
	res := queryBoundaryAsync(t, dir, "Publish")
	if !res.IdentityPartial {
		t.Errorf("键不一致必须 identity_partial: %+v", res.Chains)
	}
	found := false
	for _, d := range res.Diagnostics {
		if d.Code == "boundary_key_mismatch" && strings.Contains(d.Detail, "JOB-42") &&
			strings.Contains(d.Detail, "JOB-99") {
			found = true
		}
	}
	if !found {
		t.Errorf("应报告具体键不一致一跳: %+v", res.Diagnostics)
	}
	chain := recordChain(res)
	if chain == nil || chain.Verdict == action.SeqVerdictCorroborated {
		t.Errorf("不一致链不得 corroborated: %+v", res.Chains)
	}
	for _, h := range chain.Hops {
		if h.Kind == "persistence_or_queue" && (h.File == "" || h.Line == 0) {
			t.Errorf("未证明边界也应给位置: %+v", h)
		}
	}
}
