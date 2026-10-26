//go:build integration

package integration

// todo18-6：系统绑定必须在调用位置核对（配置字段覆盖/实例重赋值/别名修改/
// 分支或未知修改 → 降级；未改写配置正例保持）。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
)

func todo18Header(sendVar, recvVar string) string {
	return `package flow

type Job struct{ RecordID string }
type Bus struct{ System string }

func (b *Bus) Send(topic string, key string, v any) {}
func (b *Bus) Receive(topic string) (string, Job) { return "", Job{} }

var ` + sendVar + ` = &Bus{System: "system-a"}
var ` + recvVar + ` = &Bus{System: "system-a"}
`
}

func TestTodo18FieldOverwriteBeforeReceive(t *testing.T) {
	src := todo18Header("sender", "receiver") + `
func Publish() *Job {
	job := &Job{}
	job.RecordID = "JOB-42"
	sender.Send("jobs.created", job.RecordID, job)
	return job
}

func Consume() Job {
	receiver.System = "system-b"
	key, job := receiver.Receive("jobs.created")
	if key == "JOB-42" {
		_ = job.RecordID
	}
	return job
}
`
	dir := buildBoundaryRepo(t, src)
	res := queryBoundaryAsync(t, dir, "Publish")
	chain := recordChain(res)
	if chain == nil || chain.Boundary == nil {
		t.Fatalf("应有边界证据: %+v", res.Chains)
	}
	if chain.Verdict == action.SeqVerdictCorroborated || !res.IdentityPartial {
		t.Errorf("调用前覆盖配置不得沿用初始化快照: %+v", chain)
	}
	d := chain.Boundary.Detail + " " + chain.Boundary.ConsumerBinding
	for _, want := range []string{"L10", "L20", "L21", "system-b"} {
		if !strings.Contains(d, want) {
			t.Errorf("诊断应定位 %s: %s", want, d)
		}
	}
	if chain.Boundary.Verdict != "mismatch" && chain.Boundary.Verdict != "unverified" {
		t.Errorf("应为 mismatch/unverified: %+v", chain.Boundary)
	}
}

func TestTodo18ReceiverReassignedBeforeCall(t *testing.T) {
	src := todo18Header("sender", "receiver") + `
func Publish() *Job {
	job := &Job{}
	job.RecordID = "JOB-42"
	sender.Send("jobs.created", job.RecordID, job)
	return job
}

func Consume() Job {
	receiver = &Bus{System: "system-b"}
	key, job := receiver.Receive("jobs.created")
	if key == "JOB-42" {
		_ = job.RecordID
	}
	return job
}
`
	dir := buildBoundaryRepo(t, src)
	res := queryBoundaryAsync(t, dir, "Publish")
	chain := recordChain(res)
	if chain == nil || chain.Boundary == nil {
		t.Fatalf("应有边界证据: %+v", res.Chains)
	}
	if chain.Verdict == action.SeqVerdictCorroborated || !res.IdentityPartial {
		t.Errorf("实例重赋值后的绑定不得沿用初始化: %+v", chain)
	}
	d := chain.Boundary.ConsumerBinding
	for _, want := range []string{"L10", "L20", "L21", "system-b"} {
		if !strings.Contains(d, want) {
			t.Errorf("绑定应含 %s: %s", want, d)
		}
	}
}

func TestTodo18AliasModificationBeforeCall(t *testing.T) {
	src := todo18Header("sender", "receiver") + `
func Publish() *Job {
	job := &Job{}
	job.RecordID = "JOB-42"
	sender.Send("jobs.created", job.RecordID, job)
	return job
}

func Consume() Job {
	alias := receiver
	alias.System = "system-b"
	key, job := receiver.Receive("jobs.created")
	if key == "JOB-42" {
		_ = job.RecordID
	}
	return job
}
`
	dir := buildBoundaryRepo(t, src)
	res := queryBoundaryAsync(t, dir, "Publish")
	chain := recordChain(res)
	if chain == nil || chain.Boundary == nil {
		t.Fatalf("应有边界证据: %+v", res.Chains)
	}
	if chain.Verdict == action.SeqVerdictCorroborated || !res.IdentityPartial {
		t.Errorf("别名修改配置后不得沿用初始化: %+v", chain)
	}
	if !strings.Contains(chain.Boundary.ConsumerBinding, "system-b") ||
		!strings.Contains(chain.Boundary.ConsumerBinding, "覆盖 L21") {
		t.Errorf("应报告别名覆盖值/位置: %s", chain.Boundary.ConsumerBinding)
	}
}

func TestTodo18ConditionalModificationUnverified(t *testing.T) {
	src := todo18Header("sender", "receiver") + `
func Publish() *Job {
	job := &Job{}
	job.RecordID = "JOB-42"
	sender.Send("jobs.created", job.RecordID, job)
	return job
}

func Consume(flag bool) Job {
	if flag {
		receiver.System = "system-b"
	}
	key, job := receiver.Receive("jobs.created")
	if key == "JOB-42" {
		_ = job.RecordID
	}
	return job
}
`
	dir := buildBoundaryRepo(t, src)
	res := queryBoundaryAsync(t, dir, "Publish")
	chain := recordChain(res)
	if chain == nil || chain.Boundary == nil {
		t.Fatalf("应有边界证据: %+v", res.Chains)
	}
	if chain.Verdict == action.SeqVerdictCorroborated || !res.IdentityPartial {
		t.Errorf("条件修改无法证明调用时值: %+v", chain)
	}
	if chain.Boundary.Verdict != "unverified" ||
		!strings.Contains(chain.Boundary.ConsumerBinding, "条件分支") {
		t.Errorf("应明确条件修改缺口: %+v", chain.Boundary)
	}
}

func TestTodo18UnknownModificationCallUnverified(t *testing.T) {
	src := todo18Header("sender", "receiver") + `
func configure(b *Bus) { b.System = "system-b" }

func Publish() *Job {
	job := &Job{}
	job.RecordID = "JOB-42"
	sender.Send("jobs.created", job.RecordID, job)
	return job
}

func Consume() Job {
	configure(receiver)
	key, job := receiver.Receive("jobs.created")
	if key == "JOB-42" {
		_ = job.RecordID
	}
	return job
}
`
	dir := buildBoundaryRepo(t, src)
	res := queryBoundaryAsync(t, dir, "Publish")
	chain := recordChain(res)
	if chain == nil || chain.Boundary == nil {
		t.Fatalf("应有边界证据: %+v", res.Chains)
	}
	if chain.Verdict == action.SeqVerdictCorroborated || !res.IdentityPartial {
		t.Errorf("未知配置修改不得沿用初始化: %+v", chain)
	}
	if chain.Boundary.Verdict != "unverified" ||
		!strings.Contains(chain.Boundary.ConsumerBinding, "未解析的接收者") {
		t.Errorf("应说明未知修改缺口: %+v", chain.Boundary)
	}
}

func TestTodo18ModificationAfterCallKeepsProof(t *testing.T) {
	src := todo18Header("sender", "receiver") + `
func Publish() *Job {
	job := &Job{}
	job.RecordID = "JOB-42"
	sender.Send("jobs.created", job.RecordID, job)
	return job
}

func Consume() Job {
	key, job := receiver.Receive("jobs.created")
	receiver.System = "system-b"
	if key == "JOB-42" {
		_ = job.RecordID
	}
	return job
}
`
	dir := buildBoundaryRepo(t, src)
	res := queryBoundaryAsync(t, dir, "Publish")
	chain := recordChain(res)
	if chain == nil || chain.Boundary == nil {
		t.Fatalf("应有边界证据: %+v", res.Chains)
	}
	if chain.Verdict != action.SeqVerdictCorroborated || res.IdentityPartial {
		t.Errorf("调用后修改不得倒推此前调用失效: %+v", chain)
	}
	if strings.Contains(chain.Boundary.ConsumerBinding, "system-b") {
		t.Errorf("调用时绑定应为初始 system-a: %s", chain.Boundary.ConsumerBinding)
	}
}
