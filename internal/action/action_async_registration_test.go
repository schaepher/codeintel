package action

// todo13-6-R3：角色依据来自真实注册/绑定，名称启发式保持低等级。
//   - ScheduleLookalike 普通调用 AsyncRead → 不得 cron+registration+corroborated
//   - 无关键词入口 Advance 经 RegisterJob 回调绑定 → cron + 实际 API/位置

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

func TestAsyncRegistrationEvidenceIsRealBinding(t *testing.T) {
	const pkg = "example.com/app/jobs"
	seed := "symbol:go:" + pkg + ":(ReviewHandler).AfterReview"
	advance := "symbol:go:" + pkg + ":Advance"
	read := "symbol:go:" + pkg + ":AsyncRead"
	lookalike := "symbol:go:" + pkg + ":ScheduleLookalike"
	setup := "symbol:go:" + pkg + ":Setup"
	api := "symbol:go:" + pkg + ":(Scheduler).RegisterJob"
	nodes := []*domain.CodeEntity{
		asyncFunc(seed, "(ReviewHandler).AfterReview", 5),
		asyncFunc(advance, "Advance", 9),
		asyncFunc(read, "AsyncRead", 14),
		asyncFunc(lookalike, "ScheduleLookalike", 18),
		asyncFunc(setup, "Setup", 22),
		{ID: domain.CanonicalID(api), Kind: domain.KindMethod, Name: "(Scheduler).RegisterJob",
			FilePath: "jobs.go", LineStart: 25},
	}
	fields := []*domain.FunctionFieldSummary{
		asyncWrite(seed, pkg+".Job.RecordID"),
		asyncWrite(advance, pkg+".Job.RecordID"),
		asyncRead(read, pkg+".Job.RecordID"),
	}
	facts := []*domain.Fact{
		// 普通调用：名字含 Schedule，但没有注册绑定
		{SourceID: domain.CanonicalID(lookalike), TargetID: domain.CanonicalID(read),
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": float64(19)}},
		// 真实注册：Setup 调用注册 API（源码位置），API 持有 Advance 回调
		{SourceID: domain.CanonicalID(setup), TargetID: domain.CanonicalID(api),
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": float64(23)}},
		{SourceID: domain.CanonicalID(api), TargetID: domain.CanonicalID(advance),
			Kind: domain.FactPassesTo, Confidence: 0.8},
	}
	dir, acts := seedAsyncScopeRepo(t, nodes, fields, facts)
	res, err := acts.AsyncLifecycle(AsyncLifecycleRequest{Seed: seed, RepoAbs: dir})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]AsyncNode{}
	for _, n := range res.Nodes {
		byName[n.Name] = n
	}
	adv, ok := byName["Advance"]
	if !ok {
		t.Fatalf("注册回调 Advance 应进入候选: %+v", res.Nodes)
	}
	if adv.Role != AsyncRoleCron || adv.Verdict != SeqVerdictCorroborated ||
		!strings.HasPrefix(adv.RoleEvidence, "registration:") ||
		!strings.Contains(adv.RoleEvidence, "@flow.go:23") {
		t.Errorf("真实注册应 cron + 依据含 API/位置: %+v", adv)
	}
	rd, ok := byName["AsyncRead"]
	if !ok {
		t.Fatalf("普通调用目标应保留: %+v", res.Nodes)
	}
	if rd.Role == AsyncRoleCron || rd.RoleEvidence != "" || rd.Verdict == SeqVerdictCorroborated {
		t.Errorf("无注册的普通调用不得升级为 cron/corroborated: %+v", rd)
	}
}

// 只有调用方名字含 schedule、没有任何注册绑定 → 目标无角色证据。
func TestAsyncRegistrationIndexIgnoresCallerName(t *testing.T) {
	const pkg = "example.com/app/jobs"
	read := "symbol:go:" + pkg + ":AsyncRead"
	lookalike := "symbol:go:" + pkg + ":ScheduleLookalike"
	nodes := []*domain.CodeEntity{
		asyncFunc(read, "AsyncRead", 14),
		asyncFunc(lookalike, "ScheduleLookalike", 18),
	}
	facts := []*domain.Fact{
		{SourceID: domain.CanonicalID(lookalike), TargetID: domain.CanonicalID(read),
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": float64(19)}},
	}
	_, acts := seedAsyncScopeRepo(t, nodes, nil, facts)
	reg := acts.asyncRegistrationIndex()
	if _, ok := reg[read]; ok {
		t.Errorf("仅调用方名字含 schedule 不构成注册证据: %+v", reg)
	}
}
