package cli

// R77 `query architecture`——整体架构图（wiki 概览架构图节转命令）：
// 三层架构（接入层→领域层→存储层，domains 配置时领域层为领域聚合
// 节点 + 外部接口聚合节点）。--format mermaid（默认）| plantuml；
// --json 结构化；--yaml 读 wiki.yaml（domains/architecture）。

import (
	"fmt"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// architectureOut 架构图输出契约（cmd --json / MCP 共用）。
type architectureOut struct {
	Modules  int      `json:"modules"`            // 参与聚合的模块数
	Domains  []string `json:"domains,omitempty"`  // 配置的业务域（领域层聚合）
	Mermaid  string   `json:"mermaid"`            // mermaid 文本（--format mermaid/plantuml 可再转）
	Plantuml string   `json:"plantuml,omitempty"` // plantuml 转换（--format plantuml 时）
}

// architectureData 计算架构图（wiki 概览同款：archLayeredMermaid——
// yaml architecture 存在时优先用配置）。R9x：结果装配迁 action
// （Actions.Architecture）；mermaid fallback 生成与 plantuml 转换
// （渲染）留 cli。
func architectureData(acts *action.Actions, data []*domain.WikiModule, cfg wikiConfig, toPuml bool) architectureOut {
	arch := cfg.Architecture
	if arch == "" {
		arch = archMermaidFallback(data, cfg.Domains, acts)
	}
	var doms []string
	for _, d := range cfg.Domains {
		doms = append(doms, d.Name)
	}
	out, err := acts.Architecture(action.ArchitectureRequest{Data: data, Domains: doms, Mermaid: arch})
	if err != nil {
		return architectureOut{}
	}
	ao := architectureOut{Modules: out.Modules, Domains: out.Domains, Mermaid: out.Mermaid}
	if toPuml {
		ao.Plantuml = mermaidToPlantuml(out.Mermaid)
	}
	return ao
}

// cmdQueryArchitecture 实现 `query architecture [--format mermaid|plantuml] [--json] [--yaml <file>]`。
func cmdQueryArchitecture(acts *action.Actions, repo *sqlite.Repo, data []*domain.WikiModule, cfg wikiConfig, opts outputOpts, format string) int {
	out := architectureData(acts, data, cfg, format == "plantuml")
	if opts.json {
		encodeJSON(out)
		return 0
	}
	if out.Mermaid == "" {
		fmt.Println("（无架构图数据——模块间调用为空或 yaml architecture 未配置）")
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
	// 默认文本：结构摘要
	fmt.Printf("架构图（%d 个模块", out.Modules)
	if len(out.Domains) > 0 {
		fmt.Printf("；领域层聚合：%s", joinAnd(out.Domains))
	}
	fmt.Println("）——--format mermaid|plantuml 输出图文本")
	return 0
}

// joinAnd 列表顿号连接（展示用）。
func joinAnd(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += "、"
		}
		out += x
	}
	return out
}
