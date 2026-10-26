//go:build integration

package integration

// todo15-6：边界证明必须关联实际键值/实例/过滤分支（反例集）。
//   - key != K 分支里的读取不能由 K 消息触发
//   - 实际发送键 JOB-99 vs 过滤 JOB-42 → mismatch，不拿字段定义补位
//   - 无关变量比较/无关调用常量不构成过滤依据
//   - 同字段不同实例、同函数多次发送 → 不证明唯一同记录链

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
)

func TestTodo15BoundaryNotEqualBranchExcluded(t *testing.T) {
	src := todo14BoundaryHeader + `
func Publish() *Job {
	job := &Job{}
	job.RecordID = "JOB-42"
	producer.Send("jobs.created", job.RecordID, job)
	return job
}

func Consume() Job {
	key, job := consumer.Receive("jobs.created")
	if key != "JOB-42" {
		_ = job.RecordID
	}
	return job
}
`
	dir := buildBoundaryRepo(t, src)
	res := queryBoundaryAsync(t, dir, "Publish")
	chain := recordChain(res)
	if chain == nil {
		t.Fatalf("应有身份链: %+v", res.Chains)
	}
	if chain.Verdict == action.SeqVerdictCorroborated || !res.IdentityPartial {
		t.Errorf("!= 分支读取不得升级同记录链: %+v partial=%v", chain, res.IdentityPartial)
	}
	if chain.Boundary == nil || chain.Boundary.Verdict != "excluded" ||
		chain.Boundary.FilterOp != "!=" {
		t.Errorf("应明确排除该身份路径并保留运算符: %+v", chain.Boundary)
	}
	found := false
	for _, d := range res.Diagnostics {
		if d.Code == "boundary_condition_mismatch" && strings.Contains(d.Detail, "!=") &&
			strings.Contains(d.Detail, "JOB-42") {
			found = true
		}
	}
	if !found {
		t.Errorf("应报告条件/分支不匹配: %+v", res.Diagnostics)
	}
	for _, h := range chain.Hops {
		if h.Kind == "filter" && !strings.Contains(h.Detail, "!=") {
			t.Errorf("过滤跳应保留 != 与实际位置: %+v", h)
		}
	}
}

func TestTodo15BoundaryActualSendKeyWins(t *testing.T) {
	// 字段定义为 JOB-42，但实际发送字面量是 JOB-99
	src := strings.Replace(todo14BoundaryProof,
		`producer.Send("jobs.created", job.RecordID, job)`,
		`producer.Send("jobs.created", "JOB-99", job)`, 1)
	dir := buildBoundaryRepo(t, src)
	res := queryBoundaryAsync(t, dir, "Publish")
	chain := recordChain(res)
	if chain == nil || chain.Boundary == nil {
		t.Fatalf("应有边界证据: %+v", res.Chains)
	}
	if chain.Boundary.Key != "JOB-99" || chain.Boundary.Verdict != "mismatch" {
		t.Errorf("发送跳必须用实际实参 JOB-99: %+v", chain.Boundary)
	}
	if chain.Verdict == action.SeqVerdictCorroborated || !res.IdentityPartial {
		t.Errorf("键不一致不得 corroborated: %+v", chain)
	}
	found := false
	for _, d := range res.Diagnostics {
		if d.Code == "boundary_key_mismatch" && strings.Contains(d.Detail, "JOB-99") &&
			strings.Contains(d.Detail, "JOB-42") {
			found = true
		}
	}
	if !found {
		t.Errorf("诊断应同时列实际发送键与过滤键: %+v", res.Diagnostics)
	}
	for _, h := range chain.Hops {
		if h.Kind == "persistence_or_queue" {
			if !strings.Contains(h.Detail, "JOB-99") || !strings.Contains(h.Detail, "JOB-42") {
				t.Errorf("边界跳应显示实际 99 与过滤 42 的不一致: %+v", h)
			}
			if h.Verdict == action.SeqVerdictCorroborated {
				t.Errorf("不一致跳不得 corroborated: %+v", h)
			}
		}
	}
}

func TestTodo15BoundaryUnrelatedVariable(t *testing.T) {
	src := todo14BoundaryHeader + `
func Publish() *Job {
	job := &Job{}
	job.RecordID = "JOB-42"
	producer.Send("jobs.created", job.RecordID, job)
	return job
}

func Consume() Job {
	_, job := consumer.Receive("jobs.created")
	other := "JOB-42"
	if other == "JOB-42" {
		_ = job.RecordID
	}
	return job
}
`
	dir := buildBoundaryRepo(t, src)
	res := queryBoundaryAsync(t, dir, "Publish")
	chain := recordChain(res)
	if chain == nil {
		t.Fatalf("应有身份链: %+v", res.Chains)
	}
	if chain.Verdict == action.SeqVerdictCorroborated || !res.IdentityPartial {
		t.Errorf("无关变量比较不得作为过滤依据: %+v", chain)
	}
	found := false
	for _, d := range res.Diagnostics {
		if d.Code == "boundary_identity_unproven" &&
			(strings.Contains(d.Detail, "Receive 返回键") || strings.Contains(d.Detail, "值依赖")) {
			found = true
		}
	}
	if !found {
		t.Errorf("应说明过滤键缺少 Receive 值依赖: %+v", res.Diagnostics)
	}
}

func TestTodo15BoundaryUnrelatedCallConstant(t *testing.T) {
	// 无关调用出现 JOB-42，但真实过滤是 JOB-7
	src := todo14BoundaryHeader + `
func log(s string) {}

func Publish() *Job {
	job := &Job{}
	job.RecordID = "JOB-42"
	producer.Send("jobs.created", job.RecordID, job)
	return job
}

func Consume() Job {
	key, job := consumer.Receive("jobs.created")
	log("JOB-42")
	if key == "JOB-7" {
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
	if chain.Boundary.Key != "JOB-42" || chain.Boundary.Verdict != "mismatch" {
		t.Errorf("无关调用常量不得替换实际过滤键: %+v", chain.Boundary)
	}
	found := false
	for _, d := range res.Diagnostics {
		if d.Code == "boundary_key_mismatch" && strings.Contains(d.Detail, "JOB-7") {
			found = true
		}
	}
	if !found {
		t.Errorf("应以真实过滤键 JOB-7 报不一致: %+v", res.Diagnostics)
	}
}

func TestTodo15BoundaryDifferentInstances(t *testing.T) {
	src := todo14BoundaryHeader + `
func Publish() *Job {
	a := &Job{}
	b := &Job{}
	a.RecordID = "JOB-A"
	b.RecordID = "JOB-B"
	producer.Send("jobs.created", b.RecordID, b)
	return b
}

func Consume() Job {
	key, job := consumer.Receive("jobs.created")
	if key == "JOB-A" {
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
	if chain.Boundary.Key != "JOB-B" || chain.Boundary.Verdict != "mismatch" {
		t.Errorf("发送键必须取实际实例 b 的 JOB-B: %+v", chain.Boundary)
	}
	if chain.Verdict == action.SeqVerdictCorroborated {
		t.Errorf("同字段不同实例不得证明: %+v", chain)
	}
}

func TestTodo15BoundaryMultipleSendsAmbiguous(t *testing.T) {
	src := todo14BoundaryHeader + `
func Publish(flag bool) *Job {
	job := &Job{}
	job.RecordID = "JOB-42"
	if flag {
		producer.Send("jobs.created", "JOB-1", job)
	} else {
		producer.Send("jobs.created", "JOB-2", job)
	}
	return job
}

func Consume() Job {
	key, job := consumer.Receive("jobs.created")
	if key == "JOB-1" {
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
	if chain.Boundary.Verdict != "ambiguous" || chain.Verdict == action.SeqVerdictCorroborated {
		t.Errorf("同函数多次发送不同实参应 ambiguous: %+v", chain)
	}
	found := false
	for _, d := range res.Diagnostics {
		if d.Code == "boundary_key_ambiguous" && strings.Contains(d.Detail, "多个发送") {
			found = true
		}
	}
	if !found {
		t.Errorf("应报告多次发送歧义: %+v", res.Diagnostics)
	}
}
