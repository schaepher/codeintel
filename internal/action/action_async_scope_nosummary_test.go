package action

// todo13-5：无字段摘要的 seed 不得退回全包候选（大仓反例）。
//   - seed 无摘要：候选 = 调用/值身份（真实 Read 保留，Unrelated* 排除）
//   - 候选总数不是全包函数数；预算/排序/partial 语义保持
//   - 有字段摘要的 Entry 对照仍为 2 节点/1 关系，因果等级不提升

import (
	"fmt"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

func TestAsyncNoSummarySeedUsesCallIdentity(t *testing.T) {
	const pkg = "example.com/large"
	seed := "symbol:go:" + pkg + ":ScheduleOnly"
	read := "symbol:go:" + pkg + ":Read"
	entry := "symbol:go:" + pkg + ":Entry"
	nodes := []*domain.CodeEntity{
		asyncFunc(seed, "ScheduleOnly", 5),
		asyncFunc(read, "Read", 9),
		asyncFunc(entry, "Entry", 12),
	}
	fields := []*domain.FunctionFieldSummary{
		asyncRead(read, pkg+".Job.RecordID"),
		asyncWrite(entry, pkg+".Job.RecordID"),
	}
	// 2000 个无关类型（同包，字段名 Cache 与 seed 无任何关系）
	for i := 0; i < 2000; i++ {
		typeName := fmt.Sprintf("Unrelated%d", i)
		store := fmt.Sprintf("symbol:go:%s:(%s).Store", pkg, typeName)
		rd := fmt.Sprintf("symbol:go:%s:(%s).Read", pkg, typeName)
		nodes = append(nodes, asyncFunc(store, "("+typeName+").Store", 20+i*3),
			asyncFunc(rd, "("+typeName+").Read", 21+i*3))
		fields = append(fields,
			asyncWrite(store, fmt.Sprintf("%s.%s.Cache", pkg, typeName)),
			asyncRead(rd, fmt.Sprintf("%s.%s.Cache", pkg, typeName)))
	}
	facts := []*domain.Fact{
		{SourceID: domain.CanonicalID(seed), TargetID: domain.CanonicalID(read),
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": float64(6)}},
	}
	dir, acts := seedAsyncScopeRepo(t, nodes, fields, facts)
	res, err := acts.AsyncLifecycle(AsyncLifecycleRequest{Seed: seed, RepoAbs: dir})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, n := range res.Nodes {
		ids[n.ID] = true
		if strings.Contains(n.ID, "Unrelated") {
			t.Errorf("无关类型不得进入节点: %s", n.ID)
		}
	}
	if !ids[seed] || !ids[read] {
		t.Errorf("seed 与真实调用邻居必须保留: %+v", ids)
	}
	if res.CandidateTotal >= 4000 {
		t.Errorf("候选总数不得是全包函数数: %d", res.CandidateTotal)
	}
	if res.CandidateBasis != "call_identity" {
		t.Errorf("候选依据应为调用身份: %+v", res)
	}
	foundCall := false
	for _, l := range res.Links {
		if strings.Contains(l.From, "Unrelated") || strings.Contains(l.To, "Unrelated") {
			t.Errorf("无关类型之间不得生成关系: %+v", l)
		}
		if (l.From == seed && l.To == read) || (l.From == read && l.To == seed) {
			foundCall = true
		}
	}
	if !foundCall {
		t.Errorf("应保留 ScheduleOnly → Read 的真实调用关联: %+v", res.Links)
	}
	diag := false
	for _, d := range res.Diagnostics {
		if d.Code == "seed_fields_missing" {
			diag = true
		}
	}
	if !diag {
		t.Errorf("应诊断 seed 无字段摘要: %+v", res.Diagnostics)
	}

	// 有字段摘要的 Entry 对照：2 节点/1 关系，因果等级不提升
	res, err = acts.AsyncLifecycle(AsyncLifecycleRequest{Seed: entry, RepoAbs: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Nodes) != 2 || len(res.Links) != 1 {
		t.Fatalf("Entry 对照应为 2 节点/1 关系: nodes=%d links=%d", len(res.Nodes), len(res.Links))
	}
	if res.CandidateBasis != "field_identity" {
		t.Errorf("Entry 候选依据应为字段身份: %+v", res)
	}
	if res.Links[0].Verdict != SeqVerdictUnverified {
		t.Errorf("共享字段不得因范围缩小升级因果: %+v", res.Links[0])
	}
}

// 无任何关联的 seed：只有 seed + 明确诊断，不填充无关候选。
func TestAsyncNoSummarySeedNoLinks(t *testing.T) {
	const pkg = "example.com/large"
	seed := "symbol:go:" + pkg + ":ScheduleOnly"
	other := "symbol:go:" + pkg + ":Other"
	nodes := []*domain.CodeEntity{asyncFunc(seed, "ScheduleOnly", 5), asyncFunc(other, "Other", 9)}
	fields := []*domain.FunctionFieldSummary{asyncWrite(other, pkg+".Unrelated.Cache")}
	dir, acts := seedAsyncScopeRepo(t, nodes, fields, nil)
	res, err := acts.AsyncLifecycle(AsyncLifecycleRequest{Seed: seed, RepoAbs: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Nodes) != 1 || res.Nodes[0].ID != seed {
		t.Errorf("无关联时只保留 seed: %+v", res.Nodes)
	}
	found := false
	for _, d := range res.Diagnostics {
		if d.Code == "no_related_links" {
			found = true
		}
	}
	if !found {
		t.Errorf("应明确说明未发现关联: %+v", res.Diagnostics)
	}
}
