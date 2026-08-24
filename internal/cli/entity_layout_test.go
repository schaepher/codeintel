package cli

// R15 实体布局测试：调用方向拓扑排序——线尽量从左往右（上游在左、
// 下游在右；环回退原序；同层稳定）。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestSortEntitiesByCallFlow：入度 0（上游）在左，下游在右。
func TestSortEntitiesByCallFlow(t *testing.T) {
	nodes := []*domain.EntityNode{
		{ID: "E", Name: "E", Pkg: "p"},
		{ID: "B", Name: "B", Pkg: "p"},
		{ID: "A", Name: "A", Pkg: "p"},
		{ID: "C", Name: "C", Pkg: "p"},
		{ID: "D", Name: "D", Pkg: "p"},
	}
	edges := []*domain.EntityEdge{
		{From: "A", To: "B", Count: 3},
		{From: "B", To: "C", Count: 2},
		{From: "A", To: "C", Count: 1},
		{From: "D", To: "C", Count: 1},
		// E 孤立（无边）
	}
	out := sortEntitiesByCallFlow(nodes, edges)
	var got []string
	for _, n := range out {
		got = append(got, n.ID)
	}
	// A、D 入度 0 在最左；E 孤立随原序；C 在最右
	pos := map[string]int{}
	for i, id := range got {
		pos[id] = i
	}
	if pos["C"] != len(got)-1 {
		t.Errorf("C（最下游）应在最右: %v", got)
	}
	if pos["A"] > pos["B"] || pos["B"] > pos["C"] {
		t.Errorf("A→B→C 应左到右: %v", got)
	}
	if pos["E"] == -1 {
		t.Errorf("孤立 E 应保留: %v", got)
	}
	// A、D 同层（都入度 0）——稳定（保持原序 A 在 D 前）
	if pos["A"] > pos["D"] {
		t.Errorf("同层应保持原序: %v", got)
	}
}

// TestSortEntitiesByCallFlowCycle：环回退——不丢节点。
func TestSortEntitiesByCallFlowCycle(t *testing.T) {
	nodes := []*domain.EntityNode{
		{ID: "X", Name: "X", Pkg: "p"},
		{ID: "Y", Name: "Y", Pkg: "p"},
		{ID: "Z", Name: "Z", Pkg: "p"},
	}
	edges := []*domain.EntityEdge{
		{From: "X", To: "Y", Count: 1},
		{From: "Y", To: "Z", Count: 1},
		{From: "Z", To: "X", Count: 1}, // 环
	}
	out := sortEntitiesByCallFlow(nodes, edges)
	if len(out) != 3 {
		t.Fatalf("环场景不应丢节点: %v", out)
	}
}

// TestEntityMermaidFilter：全图弱关联边过滤（count < 3 不画）+
// 孤立节点隐藏——概览实体图聚焦真实协作。
func TestEntityMermaidFilter(t *testing.T) {
	g := &domain.EntityGraph{
		Nodes: []*domain.EntityNode{
			{ID: "A", Name: "A", Pkg: "p", Kind: domain.EntityKindStruct},
			{ID: "B", Name: "B", Pkg: "p", Kind: domain.EntityKindStruct},
			{ID: "C", Name: "C", Pkg: "p", Kind: domain.EntityKindStruct}, // 仅弱边
			{ID: "D", Name: "D", Pkg: "p", Kind: domain.EntityKindStruct}, // 孤立
		},
		Edges: []*domain.EntityEdge{
			{From: "A", To: "B", Count: 8},
			{From: "B", To: "C", Count: 1}, // 弱边（过滤后 C 成孤立）
		},
	}
	out := entityMermaid(g)
	if !strings.Contains(out, `A["A"]`) || !strings.Contains(out, `B["B"]`) {
		t.Errorf("强关联实体应保留:\n%s", out)
	}
	if strings.Contains(out, `C["C"]`) {
		t.Errorf("仅弱边实体应隐藏:\n%s", out)
	}
	if strings.Contains(out, `D["D"]`) {
		t.Errorf("孤立实体应隐藏:\n%s", out)
	}
	if strings.Contains(out, "-->|1|") {
		t.Errorf("弱边（count<3）不应画:\n%s", out)
	}
	if !strings.Contains(out, "-->|8|") {
		t.Errorf("强边应保留:\n%s", out)
	}
}

// TestEntitySubgraphFlowOrder：子图 mermaid 节点顺序 = 调用流向
// （入口 cli 最左，下游 sqlite 最右）。
func TestEntitySubgraphFlowOrder(t *testing.T) {
	g := &domain.EntityGraph{
		Nodes: []*domain.EntityNode{
			{ID: "symbol:go:example.com/sqlite:Repo", Name: "Repo", Pkg: "example.com/sqlite", Kind: domain.EntityKindStruct},
			{ID: "symbol:go:example.com/cli:cli", Name: "cli", Pkg: "example.com/cli", Kind: domain.EntityKindPkgFace, FreeFuncs: 10},
			{ID: "symbol:go:example.com/action:Actions", Name: "Actions", Pkg: "example.com/action", Kind: domain.EntityKindStruct},
		},
		Edges: []*domain.EntityEdge{
			{From: "symbol:go:example.com/cli:cli", To: "symbol:go:example.com/action:Actions", Count: 5},
			{From: "symbol:go:example.com/action:Actions", To: "symbol:go:example.com/sqlite:Repo", Count: 3},
		},
		ByName: map[string][]string{
			"cmdWiki": {"symbol:go:example.com/cli:cli"},
			"(Actions).WikiData": {"symbol:go:example.com/action:Actions"},
			"(Repo).GetAllCalls": {"symbol:go:example.com/sqlite:Repo"},
		},
	}
	steps := []domain.WikiSeqStep{
		{Caller: "cmdWiki", Callee: "(Actions).WikiData"},
		{Caller: "(Actions).WikiData", Callee: "(Repo).GetAllCalls"},
	}
	out := entitySubgraphMermaid(g, steps)
	// 节点声明顺序：cli（上游）在前，Repo（下游）在后
	iCli := strings.Index(out, `"cli（门面10）"`)
	iRepo := strings.Index(out, `"sqlite:Repo"`)
	iActions := strings.Index(out, `"action:Actions"`)
	if iCli < 0 || iRepo < 0 || iActions < 0 {
		t.Fatalf("节点缺失:\n%s", out)
	}
	if iCli > iActions || iActions > iRepo {
		t.Errorf("节点应按调用流向排列（cli 左 → Actions → Repo 右）:\n%s", out)
	}
}
