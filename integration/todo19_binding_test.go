//go:build integration

package integration

// todo19-6 B1–B4：调用时绑定必须按顺序/分支/别名实例/方法修改核对。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
)

func todo19Header(senderSystem, receiverSystem string) string {
	return `package flow

type Job struct{ RecordID string }
type Bus struct{ System string }

func (b *Bus) Send(topic string, key string, v any) {}
func (b *Bus) Receive(topic string) (string, Job) { return "", Job{} }

var sender = &Bus{System: "` + senderSystem + `"}
var receiver = &Bus{System: "` + receiverSystem + `"}

func Publish() *Job {
	job := &Job{}
	job.RecordID = "JOB-42"
	sender.Send("jobs.created", job.RecordID, job)
	return job
}
`
}

const todo19ConsumeTail = `	if key == "JOB-42" {
		_ = job.RecordID
	}
	return job
}
`

func todo19Chain(t *testing.T, src string) (*action.AsyncLifecycleResult, *action.AsyncChain) {
	t.Helper()
	dir := buildBoundaryRepo(t, src)
	res := queryBoundaryAsync(t, dir, "Publish")
	chain := recordChain(res)
	if chain == nil || chain.Boundary == nil {
		t.Fatalf("应有边界证据: %+v", res.Chains)
	}
	return &res, chain
}

// B1：调用后重赋值不能倒推此前调用。
func TestTodo19B1ReassignAfterCallKeepsProof(t *testing.T) {
	src := todo19Header("system-a", "system-a") + `
func Consume() Job {
	key, job := receiver.Receive("jobs.created")
	if key == "JOB-42" {
		_ = job.RecordID
	}
	receiver = &Bus{System: "system-b"}
	return job
}
`
	res, chain := todo19Chain(t, src)
	if chain.Verdict != action.SeqVerdictCorroborated || res.IdentityPartial {
		t.Errorf("调用后重赋值不得改变此前绑定: %+v", chain)
	}
	if strings.Contains(chain.Boundary.ConsumerBinding, "system-b") ||
		strings.Contains(chain.Boundary.ConsumerBinding, "重赋值") {
		t.Errorf("调用时绑定应为初始化 system-a: %s", chain.Boundary.ConsumerBinding)
	}
}

// B2：条件分支内重赋值 → 整体不得 corroborated。
func TestTodo19B2ConditionalReassignUnverified(t *testing.T) {
	src := todo19Header("system-b", "system-a") + `
func Consume(flag bool) Job {
	if flag {
		receiver = &Bus{System: "system-b"}
	}
	key, job := receiver.Receive("jobs.created")
` + todo19ConsumeTail
	res, chain := todo19Chain(t, src)
	if chain.Verdict == action.SeqVerdictCorroborated || !res.IdentityPartial {
		t.Errorf("条件重赋值不得整体升级: %+v", chain)
	}
	d := chain.Boundary.Detail + " " + chain.Boundary.ConsumerBinding
	if !strings.Contains(d, "条件分支") && !strings.Contains(d, "调用时值未知") {
		t.Errorf("应说明条件/未知: %s", d)
	}
	if !strings.Contains(d, "重赋值") || !strings.Contains(d, "L") {
		t.Errorf("应定位重赋值与调用: %s", d)
	}
}

// B3：旧实例别名的修改不能作用于重赋值后的新实例。
func TestTodo19B3AliasOldInstanceIgnored(t *testing.T) {
	src := todo19Header("system-c", "system-a") + `
func Consume() Job {
	alias := receiver
	receiver = &Bus{System: "system-b"}
	alias.System = "system-c"
	key, job := receiver.Receive("jobs.created")
` + todo19ConsumeTail
	res, chain := todo19Chain(t, src)
	if chain.Verdict == action.SeqVerdictCorroborated || !res.IdentityPartial {
		t.Errorf("旧别名修改不得作用于新实例: %+v", chain)
	}
	b := chain.Boundary
	if strings.Contains(b.ConsumerBinding, "system-c") {
		t.Errorf("当前接收者应为新实例 system-b，不应被旧别名改成 c: %s", b.ConsumerBinding)
	}
	if !strings.Contains(b.ConsumerBinding, "system-b") ||
		(b.Verdict != "mismatch" && b.Verdict != "unverified") {
		t.Errorf("应为 system-b 的 mismatch/unverified: %+v", b)
	}
}

// B4：接收者方法调用修改配置 → 旧绑定失效。
func TestTodo19B4ReceiverMethodModificationUnverified(t *testing.T) {
	src := todo19Header("system-a", "system-a") + `
func (b *Bus) Configure(system string) { b.System = system }

func Consume() Job {
	receiver.Configure("system-b")
	key, job := receiver.Receive("jobs.created")
` + todo19ConsumeTail
	res, chain := todo19Chain(t, src)
	if chain.Verdict == action.SeqVerdictCorroborated || !res.IdentityPartial {
		t.Errorf("接收者方法可能改配置，不得继续用初始化值升级: %+v", chain)
	}
	d := chain.Boundary.Detail + " " + chain.Boundary.ConsumerBinding
	if !strings.Contains(d, "Configure") && !strings.Contains(d, "方法") {
		t.Errorf("应说明方法调用可能修改配置: %s", d)
	}
	if !strings.Contains(d, "L") {
		t.Errorf("应定位方法与 Receive: %s", d)
	}
}

// 对照：删除 B1 的调用后重赋值 → 未改写正例仍 corroborated。
func TestTodo19ControlUnmodifiedKeepsProof(t *testing.T) {
	src := todo19Header("system-a", "system-a") + `
func Consume() Job {
	key, job := receiver.Receive("jobs.created")
` + todo19ConsumeTail
	res, chain := todo19Chain(t, src)
	if chain.Verdict != action.SeqVerdictCorroborated || res.IdentityPartial {
		t.Errorf("未改写配置应保持证明: %+v", chain)
	}
}
