package cli

// R9 query entities：实体协作图（类型实体 + 包门面实体 + 实体间聚合
// 边 + 实体内互调）+ 4 类设计诊断——从函数级调用链上移到对象协作
// 层面，反向暴露设计问题（高耦合/循环/上帝对象/游离函数占比）。

import (
	"fmt"
	"os"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
)

// cmdEntities 输出实体协作图：默认文本（诊断清单 + 实体表），
// --json 结构化，--format mermaid 协作图。
func cmdEntities(acts *action.Actions, opts outputOpts, format string) int {
	g, err := acts.Entities()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if opts.json {
		encodeJSON(g)
		return 0
	}
	if format == "mermaid" {
		fmt.Println(entityMermaid(g))
		return 0
	}
	// 默认文本：诊断清单 + 实体表
	printEntityDiags(g)
	fmt.Println("实体协作（全部）:")
	for _, n := range g.Nodes {
		inner := ""
		if n.InnerCalls > 0 {
			inner = fmt.Sprintf(" · 内互调 %d", n.InnerCalls)
		}
		kind := n.Kind
		if n.Kind == domain.EntityKindPkgFace {
			kind = fmt.Sprintf("门面(%d 游离函数)", n.FreeFuncs)
		}
		fmt.Printf("  %-28s %-16s %d 方法 · %d 出边%s\n",
			n.Pkg+":"+n.Name, kind, n.MethodCount, n.OutCalls, inner)
	}
	fmt.Printf("共 %d 实体 · %d 条实体间边 · %d 条诊断\n", len(g.Nodes), len(g.Edges), len(g.Diags))
	return 0
}

// printEntityDiags 设计诊断清单（默认文本输出首块，含解读与行动建议）。
func printEntityDiags(g *domain.EntityGraph) {
	if len(g.Diags) == 0 {
		fmt.Println("设计诊断: 无（全部阈值内）")
		return
	}
	labels := map[string]string{
		domain.DiagCoupled:   "高耦合对",
		domain.DiagCycle:     "循环依赖",
		domain.DiagGodObject: "上帝对象",
		domain.DiagFaceHeavy: "游离函数占比",
	}
	fmt.Println("设计诊断（信号 → 行动）:")
	for _, d := range g.Diags {
		fmt.Printf("  [%s] %s: %s\n", labels[d.Kind], d.Target, d.Detail)
		exp := entityDiagExplain[d.Kind]
		if exp[0] != "" {
			fmt.Printf("     含义: %s\n", exp[0])
			fmt.Printf("     建议: %s\n", exp[1])
		}
	}
	fmt.Println("  诊断是设计信号不是结论——先核实具体调用（query callees）再重构")
}

// entityMermaid 实体协作图 mermaid（graph LR：节点 + 聚合计数边）。
func entityMermaid(g *domain.EntityGraph) string {
	if len(g.Nodes) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("graph LR\n")
	for _, n := range g.Nodes {
		label := n.Name
		if n.Kind == domain.EntityKindPkgFace {
			label += fmt.Sprintf("（门面%d）", n.FreeFuncs)
		} else if n.InnerCalls > 0 {
			label += fmt.Sprintf("（内%d）", n.InnerCalls)
		}
		b.WriteString(fmt.Sprintf("  %s[\"%s\"]\n", entityNodeID(n.ID), label))
	}
	for _, e := range g.Edges {
		b.WriteString(fmt.Sprintf("  %s -->|%d| %s\n", entityNodeID(e.From), e.Count, entityNodeID(e.To)))
	}
	return b.String()
}

// entityNodeID 实体 ID → mermaid 节点 id（`[cli]` 纯方括号非法——
// 用全限定短 ID，防不同类型短名冲突）。
func entityNodeID(id string) string {
	return strings.NewReplacer(":", "_", ".", "_", "(", "_", ")", "_", "/", "_").Replace(id)
}
