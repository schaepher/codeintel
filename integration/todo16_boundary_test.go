//go:build integration

package integration

// todo16-6：接收侧具体值/实例 + 真实 API 身份反例与正例。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
)

func TestTodo16ReceiverReadsOtherInstance(t *testing.T) {
	src := todo14BoundaryHeader + `
func Publish() *Job {
	job := &Job{}
	job.RecordID = "JOB-42"
	bus.Send("jobs.created", job.RecordID, job)
	return job
}

func Consume() Job {
	key, job := bus.Receive("jobs.created")
	other := Job{}
	if key == "JOB-42" {
		_ = other.RecordID
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
		t.Errorf("读取 other 实例不得证明同记录: %+v", chain)
	}
	if !strings.Contains(chain.Boundary.Detail, "other") ||
		!strings.Contains(chain.Boundary.Detail, "无值/别名关联") {
		t.Errorf("应指出读取实例 other 与载荷 job 无关联: %+v", chain.Boundary)
	}
}

func TestTodo16ReceiverKeyOverwritten(t *testing.T) {
	src := todo14BoundaryHeader + `
func Publish() *Job {
	job := &Job{}
	job.RecordID = "JOB-42"
	bus.Send("jobs.created", job.RecordID, job)
	return job
}

func Consume() Job {
	key, job := bus.Receive("jobs.created")
	key = "JOB-42"
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
		t.Errorf("key 被覆盖不得证明 Receive 返回键: %+v", chain)
	}
	if !strings.Contains(chain.Boundary.Detail, "重赋值或遮蔽") ||
		!strings.Contains(chain.Boundary.Detail, "L") {
		t.Errorf("应定位覆盖与比较位置: %+v", chain.Boundary)
	}
}

func TestTodo16ReceiverKeyShadowed(t *testing.T) {
	src := todo14BoundaryHeader + `
func Publish() *Job {
	job := &Job{}
	job.RecordID = "JOB-42"
	bus.Send("jobs.created", job.RecordID, job)
	return job
}

func Consume() Job {
	key, job := bus.Receive("jobs.created")
	{
		key := "JOB-42"
		if key == "JOB-42" {
			_ = job.RecordID
		}
	}
	_ = key
	return job
}
`
	dir := buildBoundaryRepo(t, src)
	res := queryBoundaryAsync(t, dir, "Publish")
	chain := recordChain(res)
	if chain == nil || chain.Boundary == nil {
		t.Fatalf("应有边界证据: %+v", res.Chains)
	}
	if chain.Verdict == action.SeqVerdictCorroborated {
		t.Errorf("遮蔽后的 key 不得作为 Receive 键: %+v", chain)
	}
	if !strings.Contains(chain.Boundary.Detail, "遮蔽") {
		t.Errorf("应说明遮蔽: %+v", chain.Boundary)
	}
}

func TestTodo16ReceiverDifferentReceiveResult(t *testing.T) {
	src := todo14BoundaryHeader + `
func Publish() *Job {
	job := &Job{}
	job.RecordID = "JOB-42"
	bus.Send("jobs.created", job.RecordID, job)
	return job
}

func Consume() Job {
	key, job := bus.Receive("jobs.created")
	key2, other := bus.Receive("jobs.created")
	if key == "JOB-42" {
		_ = other.RecordID
	}
	_, _ = key2, job
	return job
}
`
	dir := buildBoundaryRepo(t, src)
	res := queryBoundaryAsync(t, dir, "Publish")
	chain := recordChain(res)
	if chain == nil || chain.Boundary == nil {
		t.Fatalf("应有边界证据: %+v", res.Chains)
	}
	if chain.Verdict == action.SeqVerdictCorroborated {
		t.Errorf("不同 Receive 结果的载荷不得混用: %+v", chain)
	}
	if !strings.Contains(chain.Boundary.Detail, "未发现 Receive 返回键") &&
		!strings.Contains(chain.Boundary.Detail, "无值/别名关联") {
		t.Errorf("不同 Receive 结果的过滤/载荷不得混用: %+v", chain.Boundary)
	}
}

func TestTodo16ReceiverUnrelatedSameNameField(t *testing.T) {
	src := todo14BoundaryHeader + `
type Other struct{ RecordID string }

func Publish() *Job {
	job := &Job{}
	job.RecordID = "JOB-42"
	bus.Send("jobs.created", job.RecordID, job)
	return job
}

func Consume() Job {
	key, job := bus.Receive("jobs.created")
	o := Other{}
	if o.RecordID == "JOB-42" {
		_ = job.RecordID
	}
	_ = key
	return job
}
`
	dir := buildBoundaryRepo(t, src)
	res := queryBoundaryAsync(t, dir, "Publish")
	chain := recordChain(res)
	if chain == nil || chain.Boundary == nil {
		t.Fatalf("应有边界证据: %+v", res.Chains)
	}
	if chain.Verdict == action.SeqVerdictCorroborated {
		t.Errorf("无关同名字段比较不得作为过滤键: %+v", chain)
	}
	if !strings.Contains(chain.Boundary.Detail, "未发现 Receive 返回键") &&
		!strings.Contains(chain.Boundary.Detail, "无值/别名关联") {
		t.Errorf("应说明过滤键/读取实例缺少 Receive 值依赖: %+v", chain.Boundary)
	}
}

func TestTodo16ReceiverAliasPositive(t *testing.T) {
	src := todo14BoundaryHeader + `
func Publish() *Job {
	job := &Job{}
	job.RecordID = "JOB-42"
	bus.Send("jobs.created", job.RecordID, job)
	return job
}

func Consume() Job {
	key, job := bus.Receive("jobs.created")
	alias := job
	if key == "JOB-42" {
		_ = alias.RecordID
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
		t.Errorf("载荷别名 + 未改写键 + 相等分支应保留证明: %+v", chain)
	}
}

func TestTodo16APIDifferentSystemNotProven(t *testing.T) {
	src := `package flow

type Job struct{ RecordID string }

type BusA struct{}
type BusB struct{}

func (b *BusA) Send(topic string, key string, v any) {}
func (b *BusB) Receive(topic string) (string, Job) { return "", Job{} }

var a = &BusA{}
var b = &BusB{}

func Publish() *Job {
	job := &Job{}
	job.RecordID = "JOB-42"
	a.Send("jobs.created", job.RecordID, job)
	return job
}

func Consume() Job {
	key, job := b.Receive("jobs.created")
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
		t.Errorf("不同 API 接收者类型不得升级: %+v", chain.Boundary)
	}
	if !strings.Contains(chain.Boundary.Detail, "接收者类型不同") ||
		!strings.Contains(chain.Boundary.APIIdentity, "BusA") ||
		!strings.Contains(chain.Boundary.APIIdentity, "BusB") {
		t.Errorf("应报告 API 身份缺口: %+v", chain.Boundary)
	}
}
