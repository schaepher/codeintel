//go:build integration

package integration

// todo13-6-R3：真实注册证据 vs 名称启发式（生产索引 → CLI）。
//   - Advance（无关键词）经 RegisterJob 回调绑定 → cron + 实际 API/位置
//   - ScheduleLookalike 普通调用 AsyncRead → 不升级 cron/corroborated

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
)

func TestTodo13AsyncRoleEvidenceRealIndex(t *testing.T) {
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

func AfterReview(job *Job, requestID string) {
	job.RecordID = requestID
}
`)
	write("jobs/jobs.go", `package jobs

import "example.com/app/review"

func Advance(job *review.Job) {
	job.RecordID = "advanced"
}

func AsyncRead(job *review.Job) string {
	return job.RecordID
}

func ScheduleLookalike(job *review.Job) string {
	return AsyncRead(job)
}

type Scheduler struct{}

func (s *Scheduler) RegisterJob(name string, fn func(*review.Job)) {}

var scheduler = &Scheduler{}

func Setup() {
	scheduler.RegisterJob("advance", Advance)
}
`)
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	code, out := runCLIOut(t, "query", "async", "symbol:go:example.com/app/review:AfterReview",
		"--repo", dir, "--json")
	if code != 0 {
		t.Fatalf("async exit = %d; %s", code, out)
	}
	var res action.AsyncLifecycleResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	byName := map[string]action.AsyncNode{}
	for _, n := range res.Nodes {
		byName[n.Name] = n
	}
	adv, ok := byName["Advance"]
	if !ok {
		t.Fatalf("Advance 应进入候选: %+v", res.Nodes)
	}
	if adv.Role != action.AsyncRoleCron || adv.Verdict != action.SeqVerdictCorroborated {
		t.Errorf("真实注册应 cron/corroborated: %+v", adv)
	}
	if !strings.Contains(adv.RoleEvidence, "RegisterJob") || !strings.Contains(adv.RoleEvidence, "jobs.go:") {
		t.Errorf("依据应指向注册 API 与源码位置: %+v", adv)
	}
	rd, ok := byName["AsyncRead"]
	if !ok {
		t.Fatalf("AsyncRead 应进入候选: %+v", res.Nodes)
	}
	if rd.Role == action.AsyncRoleCron || rd.RoleEvidence != "" || rd.Verdict == action.SeqVerdictCorroborated {
		t.Errorf("ScheduleLookalike 普通调用不得升级 AsyncRead: %+v", rd)
	}
}
