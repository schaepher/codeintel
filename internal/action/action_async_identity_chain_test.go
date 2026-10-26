package action

// todo12-6：跨入口值身份链（写 → 持久化/队列边界 → 读/过滤）。
//   - 逐跳输出文件/行/依据；边界未建模 → identity_partial + 诊断
//   - 同值来源才 corroborated；同名不同源 → name_only/unverified
//   - 入口注册证据（calls 边来源名）可把无关键词入口判为 cron/consumer；
//     名称启发式保持低等级

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

func asyncSummary(fn, field string, kind domain.SummaryAccessKind, line int) *domain.FunctionFieldSummary {
	return &domain.FunctionFieldSummary{FunctionID: domain.CanonicalID(fn),
		AccessKind: kind, FieldPath: field, LineStart: line}
}

func findChain(res *AsyncLifecycleResult, field string) *AsyncChain {
	for i := range res.Chains {
		if strings.Contains(res.Chains[i].Field, field) {
			return &res.Chains[i]
		}
	}
	return nil
}

// A1/A4：身份链逐跳（写/边界/读/过滤），边界未建模独立 partial。
func TestAsyncIdentityChainHops(t *testing.T) {
	review := "symbol:go:example.com/app/review:(ReviewHandler).AfterReview"
	jobs := "symbol:go:example.com/app/jobs:Advance"
	reg := "symbol:go:example.com/app/scheduler:ScheduleAdvance"
	nodes := []*domain.CodeEntity{
		asyncFunc(review, "(ReviewHandler).AfterReview", 5),
		asyncFunc(jobs, "Advance", 9),
		asyncFunc(reg, "ScheduleAdvance", 20),
	}
	fields := []*domain.FunctionFieldSummary{
		asyncSummary(review, "example.com/app/review.Job.RecordID", domain.SummaryDirectWrite, 6),
		asyncSummary(review, "example.com/app/review.Review.State", domain.SummaryDirectWrite, 7),
		asyncSummary(jobs, "example.com/app/review.Job.RecordID", domain.SummaryDirectRead, 11),
	}
	facts := []*domain.Fact{
		{SourceID: domain.CanonicalID(reg), TargetID: domain.CanonicalID(jobs),
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": 21}},
	}
	dir, acts := seedAsyncScopeRepo(t, nodes, fields, facts)
	res, err := acts.AsyncLifecycle(AsyncLifecycleRequest{Seed: review, RepoAbs: dir})
	if err != nil {
		t.Fatal(err)
	}
	// 入口注册证据：Advance 名称像 auto，但调度注册调用方 → cron
	var jobsNode *AsyncNode
	for i := range res.Nodes {
		if res.Nodes[i].ID == jobs {
			jobsNode = &res.Nodes[i]
		}
	}
	if jobsNode == nil {
		t.Fatalf("定时入口应进入节点: %+v", res.Nodes)
	}
	if jobsNode.Role != AsyncRoleCron || !strings.HasPrefix(jobsNode.RoleEvidence, "registration:") ||
		!strings.Contains(jobsNode.RoleEvidence, "ScheduleAdvance") {
		t.Errorf("注册证据应把无关键词入口判为 cron: %+v", jobsNode)
	}
	if jobsNode.Verdict != SeqVerdictCorroborated {
		t.Errorf("注册证据应 corroborated（名称启发式才低等级）: %+v", jobsNode)
	}

	chain := findChain(res, "RecordID")
	if chain == nil {
		t.Fatalf("应有 RecordID 身份链: %+v", res.Chains)
	}
	if chain.Verdict != SeqVerdictUnverified {
		t.Errorf("边界未建模时链整体 unverified: %+v", chain)
	}
	kinds := map[string]AsyncHop{}
	for _, h := range chain.Hops {
		kinds[h.Kind] = h
	}
	w, ok := kinds["write"]
	if !ok || w.Function != review || w.Line != 6 || w.File == "" {
		t.Errorf("写跳应带函数/行/文件: %+v", chain.Hops)
	}
	if _, ok := kinds["persistence_or_queue"]; !ok {
		t.Errorf("未建模边界应显式列出: %+v", chain.Hops)
	}
	r, ok := kinds["read"]
	if !ok || r.Function != jobs || r.Line != 11 {
		t.Errorf("读跳应带函数/行: %+v", chain.Hops)
	}
	if _, ok := kinds["filter"]; !ok {
		t.Errorf("过滤/查询条件应显式标未建模: %+v", chain.Hops)
	}
	if !res.IdentityPartial {
		t.Errorf("身份链不完整应 identity_partial: %+v", res)
	}
	foundDiag := false
	for _, d := range res.Diagnostics {
		if d.Code == "persistence_boundary_unverified" {
			foundDiag = true
		}
	}
	if !foundDiag {
		t.Errorf("应独立诊断持久化边界未建模: %+v", res.Diagnostics)
	}
}

// A2/R2：同名不同源 → name_only/unverified（不能判为同一生命周期）。
func TestAsyncIdentityDifferentOrigin(t *testing.T) {
	a := "symbol:go:example.com/app/review:A"
	b := "symbol:go:example.com/app/jobs:B"
	nodes := []*domain.CodeEntity{asyncFunc(a, "A", 5), asyncFunc(b, "B", 9)}
	fields := []*domain.FunctionFieldSummary{
		asyncSummary(a, "example.com/app/review.Job.RecordID", domain.SummaryDirectWrite, 6),
		asyncSummary(b, "example.com/app/review.Job.RecordID", domain.SummaryDirectWrite, 10),
		asyncSummary(b, "example.com/app/review.Job.RecordID", domain.SummaryDirectRead, 11),
	}
	origins := []*domain.SummaryOrigin{
		{FunctionID: domain.CanonicalID(a), AccessKind: domain.SummaryDirectWrite,
			FieldPath: "example.com/app/review.Job.RecordID", CallLine: 6, CalleeID: "symbol:go:example.com/app:makeA"},
		{FunctionID: domain.CanonicalID(b), AccessKind: domain.SummaryDirectWrite,
			FieldPath: "example.com/app/review.Job.RecordID", CallLine: 10, CalleeID: "symbol:go:example.com/app:makeB"},
	}
	dir := t.TempDir()
	dir, acts := seedAsyncScopeRepoOrigins(t, nodes, fields, nil, origins)
	res, err := acts.AsyncLifecycle(AsyncLifecycleRequest{Seed: a, RepoAbs: dir})
	if err != nil {
		t.Fatal(err)
	}
	chain := findChain(res, "RecordID")
	if chain == nil {
		t.Fatalf("应有身份链: %+v", res.Chains)
	}
	if chain.Verdict != SeqVerdictUnverified {
		t.Errorf("同名不同源必须 unverified: %+v", chain)
	}
	var link *AsyncLink
	for i := range res.Links {
		if strings.Contains(res.Links[i].Reason, "RecordID") {
			link = &res.Links[i]
		}
	}
	if link == nil || !strings.HasPrefix(link.Reason, "name_only") || link.EvidenceKind != "name_only" {
		t.Errorf("同名不同源应 name_only: %+v", link)
	}
}

// R2：值来源一致 → value_origin/corroborated 链。
func TestAsyncIdentitySameOriginCorroborated(t *testing.T) {
	a := "symbol:go:example.com/app/review:A"
	b := "symbol:go:example.com/app/jobs:B"
	nodes := []*domain.CodeEntity{asyncFunc(a, "A", 5), asyncFunc(b, "B", 9)}
	fields := []*domain.FunctionFieldSummary{
		asyncSummary(a, "example.com/app/review.Job.RecordID", domain.SummaryDirectWrite, 6),
		asyncSummary(b, "example.com/app/review.Job.RecordID", domain.SummaryDirectWrite, 10),
		asyncSummary(b, "example.com/app/review.Job.RecordID", domain.SummaryDirectRead, 11),
	}
	origin := &domain.SummaryOrigin{AccessKind: domain.SummaryDirectWrite,
		FieldPath: "example.com/app/review.Job.RecordID", CallLine: 6,
		CalleeID: "symbol:go:example.com/app:makeID"}
	origins := []*domain.SummaryOrigin{
		{FunctionID: domain.CanonicalID(a), AccessKind: origin.AccessKind, FieldPath: origin.FieldPath,
			CallLine: origin.CallLine, CalleeID: origin.CalleeID},
		{FunctionID: domain.CanonicalID(b), AccessKind: origin.AccessKind, FieldPath: origin.FieldPath,
			CallLine: origin.CallLine, CalleeID: origin.CalleeID},
	}
	dir, acts := seedAsyncScopeRepoOrigins(t, nodes, fields, nil, origins)
	res, err := acts.AsyncLifecycle(AsyncLifecycleRequest{Seed: a, RepoAbs: dir})
	if err != nil {
		t.Fatal(err)
	}
	var link *AsyncLink
	for i := range res.Links {
		if strings.HasPrefix(res.Links[i].Reason, "value_origin") {
			link = &res.Links[i]
		}
	}
	if link == nil || link.Verdict != SeqVerdictCorroborated || link.EvidenceKind != "value_origin" {
		t.Fatalf("同值来源应 value_origin/corroborated: %+v", res.Links)
	}
	chain := findChain(res, "RecordID")
	if chain == nil || chain.Verdict != SeqVerdictCorroborated {
		t.Errorf("链应 corroborated: %+v", chain)
	}
	if res.IdentityPartial {
		t.Errorf("值来源一致的链不应 identity_partial: %+v", res)
	}
}
