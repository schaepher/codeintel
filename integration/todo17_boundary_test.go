//go:build integration

package integration

// todo17-6：载荷覆盖后的新值 + 同类型不同系统配置的边界身份。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
)

func TestTodo17PayloadOverwrittenAfterReceive(t *testing.T) {
	src := todo14BoundaryHeader + `
func Publish() *Job {
	job := &Job{}
	job.RecordID = "JOB-42"
	bus.Send("jobs.created", job.RecordID, job)
	return job
}

func Consume() Job {
	key, job := bus.Receive("jobs.created")
	job = Job{}
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
		t.Errorf("载荷覆盖后不得证明同记录: %+v", chain)
	}
	d := chain.Boundary.Detail
	if !strings.Contains(d, "覆盖定义") || !strings.Contains(d, "Receive 返回载荷") ||
		!strings.Contains(d, "L") {
		t.Errorf("应定位 Receive/覆盖/读取: %+v", chain.Boundary)
	}
}

func TestTodo17SameTypeDifferentSystemsMismatch(t *testing.T) {
	src := `package flow

type Job struct{ RecordID string }
type Bus struct{ System string }

func (b *Bus) Send(topic string, key string, v any) {}
func (b *Bus) Receive(topic string) (string, Job) { return "", Job{} }

var sender = &Bus{System: "system-a"}
var receiver = &Bus{System: "system-b"}

func Publish() *Job {
	job := &Job{}
	job.RecordID = "JOB-42"
	sender.Send("jobs.created", job.RecordID, job)
	return job
}

func Consume() Job {
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
		t.Errorf("不同系统配置不得升级: %+v", chain)
	}
	if chain.Boundary.Verdict != "mismatch" ||
		!strings.Contains(chain.Boundary.Detail, "系统/配置绑定不同") ||
		!strings.Contains(chain.Boundary.ProducerBinding, "system-a") ||
		!strings.Contains(chain.Boundary.ConsumerBinding, "system-b") {
		t.Errorf("应报告两端绑定不同: %+v", chain.Boundary)
	}
	if !strings.Contains(chain.Boundary.APIIdentity, "Bus") {
		t.Errorf("应保留实际 API 类型身份: %+v", chain.Boundary)
	}
}

func TestTodo17UnknownBindingNotProven(t *testing.T) {
	src := `package flow

type Job struct{ RecordID string }
type Bus struct{ System string }

func (b *Bus) Send(topic string, key string, v any) {}
func (b *Bus) Receive(topic string) (string, Job) { return "", Job{} }

var sender = &Bus{}
var receiver = &Bus{}

func Publish() *Job {
	job := &Job{}
	job.RecordID = "JOB-42"
	sender.Send("jobs.created", job.RecordID, job)
	return job
}

func Consume() Job {
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
		t.Errorf("未知系统绑定不得升级: %+v", chain)
	}
	if chain.Boundary.Verdict != "unverified" ||
		!strings.Contains(chain.Boundary.Detail, "绑定未建模") {
		t.Errorf("应明确绑定缺口: %+v", chain.Boundary)
	}
}

func TestTodo17SameConfigDifferentInstancesProven(t *testing.T) {
	src := `package flow

type Job struct{ RecordID string }
type Bus struct{ System string }

func (b *Bus) Send(topic string, key string, v any) {}
func (b *Bus) Receive(topic string) (string, Job) { return "", Job{} }

var sender = &Bus{System: "system-a"}
var receiver = &Bus{System: "system-a"}

func Publish() *Job {
	job := &Job{}
	job.RecordID = "JOB-42"
	sender.Send("jobs.created", job.RecordID, job)
	return job
}

func Consume() Job {
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
	if chain.Verdict != action.SeqVerdictCorroborated || res.IdentityPartial {
		t.Errorf("同类型 + 同配置字面量应可证明: %+v", chain)
	}
	if chain.Boundary.Verdict != action.SeqVerdictCorroborated ||
		!strings.Contains(chain.Boundary.Detail, "同配置字面量") {
		t.Errorf("应展示绑定依据: %+v", chain.Boundary)
	}
}
