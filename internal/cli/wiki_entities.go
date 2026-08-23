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

// entitySequenceMermaid 实体级时序图（R12）：函数级调用链（有序）
// 投影到实体——保留调用顺序、连续相同实体对合并计数（×N）、实体
// 内调用折叠（内互调已在节点标注）。参与者带包前缀消歧。
func entitySequenceMermaid(g *domain.EntityGraph, steps []domain.WikiSeqStep) string {
	if g == nil || len(g.ByName) == 0 {
		return ""
	}
	// 1. steps → 实体对序列（保留顺序；无映射/实体内调用跳过）
	type pair struct{ from, to string }
	var seq []pair
	firstEntity := func(sym string) string {
		for _, eid := range g.ByName[sym] {
			return eid
		}
		return ""
	}
	for _, st := range steps {
		from, to := firstEntity(st.Caller), firstEntity(st.Callee)
		if from == "" || to == "" || from == to {
			continue
		}
		seq = append(seq, pair{from, to})
	}
	if len(seq) == 0 {
		return ""
	}
	// 2. 合并连续重复（保留顺序）
	var merged []struct {
		pair
		count int
	}
	for _, p := range seq {
		if n := len(merged); n > 0 && merged[n-1].pair == p {
			merged[n-1].count++
		} else {
			merged = append(merged, struct {
				pair
				count int
			}{p, 1})
		}
	}
	// 3. sequenceDiagram 渲染（参与者按出现顺序别名化）
	entityShort := func(id string) string {
		for _, n := range g.Nodes {
			if n.ID == id {
				if n.Kind == domain.EntityKindPkgFace {
					return shortMod(n.Pkg) + "（门面" + fmt.Sprint(n.FreeFuncs) + "）"
				}
				return shortMod(n.Pkg) + ":" + n.Name
			}
		}
		return id
	}
	alias := map[string]string{}
	var order []string
	aliasOf := func(id string) string {
		if a, ok := alias[id]; ok {
			return a
		}
		a := fmt.Sprintf("P%d", len(order))
		alias[id] = a
		order = append(order, id)
		return a
	}
	var b strings.Builder
	b.WriteString("sequenceDiagram\n")
	// 收集参与者（声明必须在消息行前）
	seen := map[string]bool{}
	for _, m := range merged {
		for _, id := range []string{m.from, m.to} {
			if !seen[id] {
				seen[id] = true
				aliasOf(id)
			}
		}
	}
	for _, id := range order {
		b.WriteString(fmt.Sprintf("  participant %s as \"%s\"\n", alias[id], entityShort(id)))
	}
	for _, m := range merged {
		label := "call"
		if m.count > 1 {
			label = fmt.Sprintf("call ×%d", m.count)
		}
		b.WriteString(fmt.Sprintf("  %s->>%s: %s\n", alias[m.from], alias[m.to], label))
	}
	return b.String()
}

// entityLegend 图例（图怎么读）。
const entityLegend = "图例：节点 = 实体（`类型名`；`门面 N` = 包内 N 个游离函数聚合，`内 N` = 实体内方法互调）；" +
	"边 `--|N|-->` = 方法互调 N 次（聚合计数）。"

// renderEntitiesSectionMD 概览「实体协作」区块（md）：全图 + 图例 +
// 设计诊断清单（含解读与行动建议）——新人第一眼看到对象协作与设计信号。
func renderEntitiesSectionMD(g *domain.EntityGraph) string {
	if g == nil || len(g.Nodes) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## 实体协作（对象设计视角）\n\n")
	b.WriteString("> 类型（有行为）为实体 + 游离函数按包聚合为门面；边 = 方法互调聚合计数。\n\n")
	b.WriteString(entityLegend + "\n\n")
	b.WriteString("```mermaid\n" + entityMermaid(g) + "\n```\n\n")
	if len(g.Diags) > 0 {
		b.WriteString("**设计诊断**（信号 → 行动；阈值见 `codeintel query entities`）：\n\n")
		labels := map[string]string{
			domain.DiagCoupled:   "高耦合对",
			domain.DiagCycle:     "循环依赖",
			domain.DiagGodObject: "上帝对象",
			domain.DiagFaceHeavy: "游离函数占比",
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
func renderEntitiesSectionHTML(g *domain.EntityGraph) string {
	if g == nil || len(g.Nodes) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<section id="entities"><h2>实体协作（对象设计视角）</h2><p class="muted">类型（有行为）为实体 + 游离函数按包聚合为门面；边 = 方法互调聚合计数。</p><p class="muted">` + htmlEsc(entityLegend) + `</p>`)
	b.WriteString("<pre class=\"mermaid\">" + htmlEsc(entityMermaid(g)) + "</pre>")
	if len(g.Diags) > 0 {
		labels := map[string]string{
			domain.DiagCoupled:   "高耦合对",
			domain.DiagCycle:     "循环依赖",
			domain.DiagGodObject: "上帝对象",
			domain.DiagFaceHeavy: "游离函数占比",
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
