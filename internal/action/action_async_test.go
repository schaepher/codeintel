package action

// todo10-6：异步生命周期关联（query async）验收。
//   - 人工审核入口与定时任务入口经**状态字段**关联（corroborated）
//   - 人工推进与内部自动推进并存：调用链证据 vs 仅同作用域（unverified）
//   - 不推断固定操作次数（只给证据，不给次数结论）

import (
	"strings"
	"testing"
)

// TestAsyncFieldLink：cross_entry_async 夹具——Approve（人工）与 Sweep
// （定时）经状态字段 State 关联（corroborated + field: 依据）。
func TestAsyncFieldLink(t *testing.T) {
	dir, acts := indexTodo10Fixture(t, "cross_entry_async")
	res, err := acts.AsyncLifecycle(AsyncLifecycleRequest{Seed: "example.com/fx/cross_entry_async:Approve", RepoAbs: dir})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]AsyncNode{}
	for _, n := range res.Nodes {
		byName[n.Name] = n
	}
	if _, ok := byName["Sweep"]; !ok {
		t.Fatalf("定时任务入口 Sweep 应进入候选: %+v", res.Nodes)
	}
	if r := byName["Approve"].Role; r != AsyncRoleHuman {
		t.Errorf("Approve 角色 = %q; want human", r)
	}
	if r := byName["Sweep"].Role; r != AsyncRoleCron {
		t.Errorf("Sweep 角色 = %q; want cron", r)
	}
	var found *AsyncLink
	for i := range res.Links {
		l := &res.Links[i]
		if strings.HasSuffix(l.From, "Approve") && strings.HasSuffix(l.To, "Sweep") ||
			strings.HasSuffix(l.From, "Sweep") && strings.HasSuffix(l.To, "Approve") {
			found = l
			break
		}
	}
	if found == nil {
		t.Fatalf("Approve 与 Sweep 应有关联: %+v", res.Links)
	}
	// todo11-3：仅"共享字段读写"是**启发式**——标 unverified + evidence_kind，
	// 只有值来源/调用路径可验证时才升级为 corroborated
	if found.Verdict != SeqVerdictUnverified || found.EvidenceKind != "field_shared" {
		t.Errorf("共享字段读写应标记为启发式（unverified/field_shared）: %+v", found)
	}
	if !strings.Contains(found.Reason, "State") {
		t.Errorf("证据应指出状态字段: %+v", found)
	}
}

// TestAsyncDualAdvance：dual_advance 夹具——内部自动推进有调用链证据；
// 人工推进与自动推进只共享作用域时标 unverified（不构成因果）。
func TestAsyncDualAdvance(t *testing.T) {
	dir, acts := indexTodo10Fixture(t, "dual_advance")
	res, err := acts.AsyncLifecycle(AsyncLifecycleRequest{Seed: "example.com/fx/dual_advance:Review", RepoAbs: dir})
	if err != nil {
		t.Fatal(err)
	}
	roles := map[string]string{}
	for _, n := range res.Nodes {
		roles[n.Name] = n.Role
	}
	if roles["AdvanceByHuman"] != AsyncRoleHuman {
		t.Errorf("AdvanceByHuman 角色 = %q; want human（%v）", roles["AdvanceByHuman"], roles)
	}
	if roles["AdvanceBySystem"] != AsyncRoleAuto && roles["Review"] != AsyncRoleAudit {
		t.Errorf("应识别审核/自动推进角色: %v", roles)
	}
	var chainLink, scopeOnly bool
	for _, l := range res.Links {
		switch {
		case l.Reason == "call_path" && l.Verdict == SeqVerdictCorroborated && l.EvidenceKind == "call_path":
			chainLink = true
		case l.Reason == "shared_scope_only" && l.Verdict == SeqVerdictUnverified:
			scopeOnly = true
		}
	}
	if !chainLink {
		t.Errorf("Review → AdvanceBySystem 应有调用路径证据（call_path）: %+v", res.Links)
	}
	if !scopeOnly {
		t.Errorf("仅共享作用域的关系必须标 unverified（不构成因果）: %+v", res.Links)
	}
	// 不推断固定操作次数：结果里不应出现次数类字段
	for _, l := range res.Links {
		if strings.Contains(l.Detail, "次") {
			t.Errorf("不应推断固定操作次数: %+v", l)
		}
	}
}

// TestAsyncPartialWriteScope：partial_write 夹具——Commit 与 CommitOverwrite
// 触碰同一 ID 类字段（key）→ 标识同源证据。
func TestAsyncPartialWriteScope(t *testing.T) {
	dir, acts := indexTodo10Fixture(t, "partial_write")
	res, err := acts.AsyncLifecycle(AsyncLifecycleRequest{Seed: "symbol:go:example.com/fx/partial_write:Commit", RepoAbs: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Nodes) < 2 {
		t.Fatalf("应有多个入口候选（含 CommitOverwrite）: %+v", res.Nodes)
	}
	var sawEvidence bool
	for _, l := range res.Links {
		if l.Verdict == SeqVerdictCorroborated {
			sawEvidence = true
		}
	}
	if !sawEvidence {
		t.Errorf("同作用域的写入函数之间应至少有一条可核验证据: %+v", res.Links)
	}
}
