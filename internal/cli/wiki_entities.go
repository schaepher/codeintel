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

// entityDiagExplain 诊断类型 → （含义, 怎么看/怎么处理）——把设计
// 信号闭环到行动（R10：新人可理解的解读层）。
var entityDiagExplain = map[string][2]string{
	domain.DiagCoupled: {
		"两个实体方法互调密集（≥20 次）——可能职责重叠或边界不清",
		"用 `query callees` 看具体调用方向，考虑接口隔离或职责重分配",
	},
	domain.DiagCycle: {
		"跨包实体循环依赖（A 依赖 B，B 又依赖 A）——分层被破坏",
		"抽公共依赖到第三包，或反转依赖方向（依赖抽象而非实现）",
	},
	domain.DiagGodObject: {
		"方法数过多（≥40）或出边过广（≥20）——职责过载",
		"按职责拆分为多个类型，或抽离被频繁调用的协作对象",
	},
	domain.DiagFaceHeavy: {
		"包内游离函数（未绑定类型）多于方法——缺少类型封装",
		"将相关游离函数归入合适的类型（方法），或定义新类型承载",
	},
}

// chainLineNum 调用边行号（metadata.line_num——AST 发射时记录调用
// 位置；缺失返回 -1）。

// sortChainByCallLine 调用链按源码调用行号排序（R13）：深度 1（入口
// 直接调用）按入口内行号；深度 2（被调者内部）按被调者在入口中的
// 调用位置 + 内部行号——还原源码书写顺序（"顺序与代码一一对应"）。
// 无行号边 fallback 排最后。

// entitySequenceMermaid 实体级时序图（R12）：函数级调用链（有序）
// 投影到实体——保留调用顺序、连续相同实体对合并计数（×N）、实体
// 内调用折叠（内互调已在节点标注）。参与者带包前缀消歧。

// sortEntitiesByCallFlow 按调用方向拓扑排序（R15）：入度 0（上游/入口）
// 在左，下游在右——调用线尽量从左往右。Kahn 算法 + 稳定（同层保持
// 输入序）；环/孤立节点回退原序（不丢节点）。
func sortEntitiesByCallFlow(nodes []*domain.EntityNode, edges []*domain.EntityEdge) []*domain.EntityNode {
	indeg := map[string]int{}
	adj := map[string][]string{}
	for _, e := range edges {
		indeg[e.To]++
		adj[e.From] = append(adj[e.From], e.To)
	}
	// 入度 0 的节点按原序入队（稳定）
	var queue []*domain.EntityNode
	for _, n := range nodes {
		if indeg[n.ID] == 0 {
			queue = append(queue, n)
		}
	}
	var out []*domain.EntityNode
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		out = append(out, n)
		for _, to := range adj[n.ID] {
			indeg[to]--
			if indeg[to] == 0 {
				for _, m := range nodes {
					if m.ID == to {
						queue = append(queue, m)
						break
					}
				}
			}
		}
	}
	// 剩余（环内/孤立）按原序追加
	seen := map[string]bool{}
	for _, n := range out {
		seen[n.ID] = true
	}
	for _, n := range nodes {
		if !seen[n.ID] {
			out = append(out, n)
		}
	}
	return out
}

// entityLegend 图例（图怎么读）。
const entityLegend = "图例：节点 = 实体（`类型名`；`门面 N` = 包内 N 个游离函数聚合，`内 N` = 实体内方法互调）；" +
	"边 `--|N|-->` = 方法互调 N 次（聚合计数）。"

// renderEntitiesSectionMD 概览「实体协作」区块（md）：全图 + 图例 +
// 设计诊断清单（含解读与行动建议）——新人第一眼看到对象协作与设计信号。
func renderEntitiesSectionMD(g *domain.EntityGraph, rc *wikiRenderCtx) string {
	if g == nil || len(g.Nodes) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## 实体协作（对象设计视角）\n\n")
	b.WriteString("> 类型（有行为）为实体 + 游离函数按包聚合为门面；边 = 方法互调聚合计数。\n\n")
	b.WriteString(entityLegend + "\n\n")
	// F2：按 DDD 领域分组——领域间关系图 + 每领域内部协作分开展示
	doms := splitEntityDomains(g, rc.cfg.Domains)
	if len(doms) >= 2 {
		b.WriteString("**领域分组**：实体按 DDD 目录（domain/service 子域）分组；领域间图 + 每领域内部图分开。\n\n")
		b.WriteString("### 领域间关系\n\n")
		b.WriteString(rc.diagramMD(domainMermaid(doms, g.Edges)))
		for _, d := range doms {
			b.WriteString(fmt.Sprintf("<details><summary>领域 <code>%s</code>（%d 实体）——展开查看内部协作</summary>\n\n",
				htmlEsc(d.Name), len(d.Nodes)))
			// R39：单实体无内部协作时渲染空 mermaid 块（自身 wiki 实测
			// "基础支撑域 1 实体"空图）——显示文字说明
			if sub := entityMermaid(&domain.EntityGraph{Nodes: d.Nodes, Edges: d.Edges}); sub != "" {
				b.WriteString(rc.diagramMD(sub))
			} else {
				b.WriteString("（无内部协作）\n\n")
			}
			b.WriteString("</details>\n\n")
		}
	} else {
		// R39：无强协作边时 entityMermaid 为空——不渲染空块
		if sub := entityMermaid(g); sub != "" {
			b.WriteString(rc.diagramMD(sub))
		} else {
			b.WriteString("（无内部协作）\n\n")
		}
	}
	if len(g.Diags) > 0 {
		b.WriteString("**设计诊断**（信号 → 行动；阈值见 `codeintel query entities`）：\n\n")
		labels := map[string]string{
			domain.DiagCoupled:	"高耦合对",
			domain.DiagCycle:	"循环依赖",
			domain.DiagGodObject:	"上帝对象",
			domain.DiagFaceHeavy:	"游离函数占比",
		}
		for _, d := range g.Diags {
			exp := entityDiagExplain[d.Kind]
			b.WriteString(fmt.Sprintf("- **[%s]** %s：%s\n", labels[d.Kind], d.Target, d.Detail))
			if exp[0] != "" {
				b.WriteString(fmt.Sprintf("  - 含义：%s\n", exp[0]))
				b.WriteString(fmt.Sprintf("  - 建议：%s\n", exp[1]))
			}
		}
		b.WriteString("\n诊断是设计信号不是结论——先核实具体调用（`query callees <符号>`）再重构。\n\n")
	}
	return b.String()
}

// renderEntitiesSectionHTML 概览「实体协作」区块（html/serve 共用）。
func renderEntitiesSectionHTML(g *domain.EntityGraph, rc *wikiRenderCtx) string {
	if g == nil || len(g.Nodes) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<section id="entities"><h2>实体协作（对象设计视角）</h2><p class="muted">类型（有行为）为实体 + 游离函数按包聚合为门面；边 = 方法互调聚合计数。</p><p class="muted">` + htmlEsc(entityLegend) + `</p>`)
	// F2：按 DDD 领域分组——领域间图 + 每领域内部图分开展示
	doms := splitEntityDomains(g, rc.cfg.Domains)
	if len(doms) >= 2 {
		b.WriteString(`<p class="muted">领域分组：业务域划分（AI 分析 → wiki.yaml domains）；领域间图 + 每领域内部图分开。</p>`)
		b.WriteString("<h3>领域间关系</h3>" + rc.diagramHTML(domainMermaid(doms, g.Edges)))
		for i, d := range doms {
			id := fmt.Sprintf("entity-dom-%d", i)
			// R39：单实体无内部协作时渲染空 mermaid 块——显示文字说明
			inner := "（无内部协作）"
			if sub := entityMermaid(&domain.EntityGraph{Nodes: d.Nodes, Edges: d.Edges}); sub != "" {
				inner = rc.diagramHTML(sub)
			}
			b.WriteString(fmt.Sprintf(`<h4 class="fold-btn" data-target="%s" data-label="1">▸ 领域 %s（%d 实体）——内部协作</h4><div class="sec-body" id="%s" style="display:none">%s</div>`,
				id, htmlEsc(d.Name), len(d.Nodes), id, inner))
		}
	} else {
		// R39：无强协作边时 entityMermaid 为空——不渲染空块
		if sub := entityMermaid(g); sub != "" {
			b.WriteString(rc.diagramHTML(sub))
		} else {
			b.WriteString("<p class=\"muted\">（无内部协作）</p>")
		}
	}
	if len(g.Diags) > 0 {
		labels := map[string]string{
			domain.DiagCoupled:	"高耦合对",
			domain.DiagCycle:	"循环依赖",
			domain.DiagGodObject:	"上帝对象",
			domain.DiagFaceHeavy:	"游离函数占比",
		}
		b.WriteString("<h3>设计诊断（信号 → 行动）</h3><ul>")
		for _, d := range g.Diags {
			exp := entityDiagExplain[d.Kind]
			b.WriteString(fmt.Sprintf("<li><strong>%s</strong> %s：%s<br><span class=\"muted\">含义：%s；建议：%s</span></li>",
				labels[d.Kind], htmlEsc(d.Target), htmlEsc(d.Detail), htmlEsc(exp[0]), htmlEsc(exp[1])))
		}
		b.WriteString("</ul><p class=\"muted\">诊断是设计信号不是结论——先核实具体调用（query callees &lt;符号&gt;）再重构。</p>")
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
