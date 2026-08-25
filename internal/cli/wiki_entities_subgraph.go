package cli

// R62 行数治理：entitySubgraphMermaid 从 wiki_entities.go 拆出（文件
// 超 300 行）。

import (
	"fmt"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// entitySubgraphMermaid 函数级调用链 → 实体协作子图 mermaid。
// steps 两端的符号短名经 ByName 映射实体；节点 = 涉及的实体，
// 边 = 集合内全局实体边聚合计数。
func entitySubgraphMermaid(g *domain.EntityGraph, steps []domain.WikiSeqStep) string {
	if g == nil || len(g.ByName) == 0 {
		return ""
	}
	involved := map[string]bool{}
	for _, st := range steps {
		for _, sym := range []string{st.Caller, st.Callee} {
			for _, eid := range g.ByName[sym] {
				involved[eid] = true
			}
		}
	}
	if len(involved) == 0 {
		return ""
	}
	// 涉及实体（按 Pkg/Name 确定性排序）
	var nodes []*domain.EntityNode
	for _, n := range g.Nodes {
		if involved[n.ID] {
			nodes = append(nodes, n)
		}
	}
	if len(nodes) == 0 {
		return ""
	}
	// 先确定性排序（Pkg/Name），再按调用方向拓扑（R15：线从左往右）
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Pkg != nodes[j].Pkg {
			return nodes[i].Pkg < nodes[j].Pkg
		}
		return nodes[i].Name < nodes[j].Name
	})
	// 集合内边聚合（拓扑排序用）
	type key struct{ from, to string }
	counts := map[key]int{}
	var subEdges []*domain.EntityEdge
	for _, e := range g.Edges {
		if involved[e.From] && involved[e.To] {
			counts[key{e.From, e.To}] += e.Count
			subEdges = append(subEdges, e)
		}
	}
	nodes = sortEntitiesByCallFlow(nodes, subEdges)
	var b strings.Builder
	b.WriteString("graph LR\n")
	for _, n := range nodes {
		label := shortMod(n.Pkg) + ":" + n.Name
		if n.Kind == domain.EntityKindPkgFace {
			label = shortMod(n.Pkg) + "（门面" + fmt.Sprint(n.FreeFuncs) + "）"
		} else if n.InnerCalls > 0 {
			label += fmt.Sprintf("（内%d）", n.InnerCalls)
		}
		b.WriteString(fmt.Sprintf("  %s[\"%s\"]\n", entityNodeID(n.ID), label))
	}
	for _, e := range g.Edges {
		if c, ok := counts[key{e.From, e.To}]; ok {
			b.WriteString(fmt.Sprintf("  %s -->|%d| %s\n", entityNodeID(e.From), c, entityNodeID(e.To)))
		}
	}
	return b.String()
}
