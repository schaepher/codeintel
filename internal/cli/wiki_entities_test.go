package cli

// R12 实体时序图测试：函数级调用链（有序）投影到实体级 sequenceDiagram
// ——保留顺序、合并连续重复、实体内调用折叠。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// entitySeqFixture 实体图 + 函数级 steps（模拟 cmdWiki 形态：入口
// 游离函数 → Actions 方法链 → Repo 方法）。
func entitySeqFixture() (*domain.EntityGraph, []domain.WikiSeqStep) {
	g := &domain.EntityGraph{
		Nodes: []*domain.EntityNode{
			{ID: "symbol:go:example.com/cli:cli", Name: "cli", Pkg: "example.com/cli", Kind: domain.EntityKindPkgFace, FreeFuncs: 10},
			{ID: "symbol:go:example.com/action:Actions", Name: "Actions", Pkg: "example.com/action", Kind: domain.EntityKindStruct, MethodCount: 20},
			{ID: "symbol:go:example.com/sqlite:Repo", Name: "Repo", Pkg: "example.com/sqlite", Kind: domain.EntityKindStruct, MethodCount: 30},
		},
		Edges: []*domain.EntityEdge{
			{From: "symbol:go:example.com/cli:cli", To: "symbol:go:example.com/action:Actions", Count: 8},
			{From: "symbol:go:example.com/action:Actions", To: "symbol:go:example.com/sqlite:Repo", Count: 5},
		},
		ByName: map[string][]string{
			"cmdWiki":      {"symbol:go:example.com/cli:cli"},
			"(Actions).WikiData": {"symbol:go:example.com/action:Actions"},
			"(Actions).Packages": {"symbol:go:example.com/action:Actions"},
			"(Repo).GetAllCalls": {"symbol:go:example.com/sqlite:Repo"},
			"(Repo).GetTables":   {"symbol:go:example.com/sqlite:Repo"},
		},
	}
	steps := []domain.WikiSeqStep{
		{Caller: "cmdWiki", Callee: "(Actions).WikiData"},
		{Caller: "cmdWiki", Callee: "(Actions).Packages"},            // 连续 cli→Actions（合并 ×2）
		{Caller: "(Actions).WikiData", Callee: "(Repo).GetAllCalls"},
		{Caller: "(Actions).Packages", Callee: "(Repo).GetTables"},  // 连续 Actions→Repo（合并 ×2）
		{Caller: "(Actions).WikiData", Callee: "(Actions).Packages"}, // 实体内调用——折叠
	}
	return g, steps
}

// TestEntitySequenceMermaid：顺序保留 + 连续合并 + 实体内折叠。
func TestEntitySequenceMermaid(t *testing.T) {
	g, steps := entitySeqFixture()
	out := entitySequenceMermaid(g, steps)
	if out == "" {
		t.Fatal("空输出")
	}
	// 参与者：cli（门面）/ Actions / Repo（3 个，按出现顺序）
	if !strings.Contains(out, `participant P0 as "cli（门面10）"`) {
		t.Errorf("缺 cli 门面参与者:\n%s", out)
	}
	if !strings.Contains(out, `participant P1 as "action:Actions"`) {
		t.Errorf("缺 Actions 参与者（带包前缀消歧）:\n%s", out)
	}
	if !strings.Contains(out, `participant P2 as "sqlite:Repo"`) {
		t.Errorf("缺 Repo 参与者:\n%s", out)
	}
	// 顺序保留 + 连续重复合并：cli→Actions ×2 → Actions→Repo ×2
	if !strings.Contains(out, "P0->>P1: call ×2") {
		t.Errorf("连续 cli→Actions 应合并为 call ×2:\n%s", out)
	}
	if !strings.Contains(out, "P1->>P2: call ×2") {
		t.Errorf("连续 Actions→Repo 应合并为 call ×2:\n%s", out)
	}
	// 实体内调用（Actions→Actions）不出现
	if strings.Contains(out, "P1->>P1") {
		t.Errorf("实体内调用应折叠:\n%s", out)
	}
}
