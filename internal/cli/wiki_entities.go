package cli

// R9 wiki 实体协作渲染：流程页/模块页的巨型函数级时序图替换为
// 实体协作子图（Q7）——函数短名 → 实体（ByName）→ 涉及实体集合
// → 集合内全局边聚合。cmdWiki 52 函数 → ~8 实体节点。

import (
	"fmt"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// renderEntitiesSectionMD 概览「实体协作」区块（md）：全图 + 设计诊断
// 清单——新人第一眼看到对象协作与设计信号。
func renderEntitiesSectionMD(g *domain.EntityGraph) string {
	if g == nil || len(g.Nodes) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## 实体协作（对象设计视角）\n\n")
	b.WriteString("> 类型（有行为）为实体 + 游离函数按包聚合为门面；边 = 方法互调聚合计数。\n\n")
	b.WriteString("```mermaid\n" + entityMermaid(g) + "\n```\n\n")
	if len(g.Diags) > 0 {
		b.WriteString("**设计诊断**（阈值见 `codeintel query entities`）：\n\n")
		labels := map[string]string{
			domain.DiagCoupled:   "高耦合对",
			domain.DiagCycle:     "循环依赖",
			domain.DiagGodObject: "上帝对象",
			domain.DiagFaceHeavy: "游离函数占比",
		}
		for _, d := range g.Diags {
			b.WriteString(fmt.Sprintf("- **[%s]** %s：%s\n", labels[d.Kind], d.Target, d.Detail))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// renderEntitiesSectionHTML 概览「实体协作」区块（html/serve 共用）。
func renderEntitiesSectionHTML(g *domain.EntityGraph) string {
	if g == nil || len(g.Nodes) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<section id="entities"><h2>实体协作（对象设计视角）</h2><p class="muted">类型（有行为）为实体 + 游离函数按包聚合为门面；边 = 方法互调聚合计数。</p>`)
	b.WriteString("<pre class=\"mermaid\">" + htmlEsc(entityMermaid(g)) + "</pre>")
	if len(g.Diags) > 0 {
		labels := map[string]string{
			domain.DiagCoupled:   "高耦合对",
			domain.DiagCycle:     "循环依赖",
			domain.DiagGodObject: "上帝对象",
			domain.DiagFaceHeavy: "游离函数占比",
		}
		b.WriteString("<h3>设计诊断</h3><ul>")
		for _, d := range g.Diags {
			b.WriteString(fmt.Sprintf("<li><strong>%s</strong> %s：%s</li>", labels[d.Kind], htmlEsc(d.Target), htmlEsc(d.Detail)))
		}
		b.WriteString("</ul>")
	}
	b.WriteString("</section>")
	return b.String()
}

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
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Pkg != nodes[j].Pkg {
			return nodes[i].Pkg < nodes[j].Pkg
		}
		return nodes[i].Name < nodes[j].Name
	})
	// 集合内边聚合
	type key struct{ from, to string }
	counts := map[key]int{}
	for _, e := range g.Edges {
		if involved[e.From] && involved[e.To] {
			counts[key{e.From, e.To}] += e.Count
		}
	}
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
