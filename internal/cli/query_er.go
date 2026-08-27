package cli

// R77 `query er`——ER 图（wiki er.md 转命令）：表间直接键关联
// （fk/query）的 erDiagram mermaid + 关系明细。--format mermaid（默认）
// | plantuml；--json 结构化；--yaml 读 wiki.yaml（tables.hidden）。

import (
	"fmt"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
)

// erOut ER 图输出契约（cmd --json / MCP 共用）。
type erOut struct {
	Mermaid   string                  `json:"mermaid"`            // erDiagram mermaid 文本
	Plantuml  string                  `json:"plantuml,omitempty"` // plantuml 转换（--format plantuml）
	Relations []*domain.TableRelation `json:"relations"`          // 关系明细（fk/query 直接键关联）
}

// erData 计算 ER 图（wiki er.md 同款：renderERMermaid——只画 fk/query）。
// 数据获取 + 未算兜底编排在 action（Actions.ERRelations，R9x 迁入）；
// 本层只做 fk/query 过滤（渲染选择）与 mermaid/plantuml 文本拼装。
func erData(acts *action.Actions, cfg wikiConfig, toPuml bool) erOut {
	out := erOut{Relations: []*domain.TableRelation{}}
	rels, err := wikiRelations(acts)
	if err != nil || len(rels) == 0 {
		return out
	}
	hide := hideTableFrom(cfg)
	for _, r := range rels {
		if r.Type != domain.RelationFK && r.Type != domain.RelationQuery {
			continue
		}
		if hide[r.FromTable] || hide[r.ToTable] {
			continue
		}
		out.Relations = append(out.Relations, r)
	}
	out.Mermaid = renderERMermaid(out.Relations, hide)
	if toPuml {
		out.Plantuml = mermaidToPlantuml(out.Mermaid)
	}
	return out
}

// cmdQueryER 实现 `query er [--format mermaid|plantuml] [--json] [--yaml <file>]`。
func cmdQueryER(acts *action.Actions, cfg wikiConfig, opts outputOpts, format string) int {
	out := erData(acts, cfg, format == "plantuml")
	if opts.json {
		encodeJSON(out)
		return 0
	}
	if len(out.Relations) == 0 {
		fmt.Println("（无表间直接键关联——fk/query 关系为空）")
		return 0
	}
	if format == "plantuml" {
		fmt.Println(out.Plantuml)
		return 0
	}
	if format == "mermaid" {
		fmt.Println(out.Mermaid)
		return 0
	}
	// 默认文本：关系明细
	fmt.Printf("ER 图（%d 条直接键关联）\n", len(out.Relations))
	for _, r := range out.Relations {
		fmt.Printf("  %s.%s → %s.%s [%s]\n", r.FromTable, r.FromCol, r.ToTable, r.ToCol, r.Type)
	}
	fmt.Println("（--format mermaid|plantuml 输出图文本）")
	return 0
}
