package cli

// R14 wiki 布局重排：核心业务流程图区块（yaml flows——手写业务
// 时序，AI 无法自动生成内容，只参与排位）。区块位置 = 实体协作后
// （新人认知路径：系统组成 → 对象协作 → 业务流转 → 怎么用）。

import (
	"strings"
)

// renderBusinessFlowsSectionMD 核心业务流程图区块（md）：yaml flows
// 全部列出——维护者手写的业务时序（AI 不自动实现，靠 yaml 补充）。
func renderBusinessFlowsSectionMD(cfg wikiConfig, rc *wikiRenderCtx) string {
	if len(cfg.Flows) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## 核心业务流程图\n\n")
	b.WriteString("> 业务时序（wiki.yaml flows 手写——维护者补充；系统调用链见下方「系统流程」）。\n\n")
	for _, f := range cfg.Flows {
		b.WriteString("### " + f.Title + "\n\n")
		b.WriteString(rc.diagramMD(f.Mermaid))
	}
	return b.String()
}

// renderBusinessFlowsSectionHTML 核心业务流程图区块（html/serve 共用）。
func renderBusinessFlowsSectionHTML(cfg wikiConfig, rc *wikiRenderCtx) string {
	if len(cfg.Flows) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<section id="flows"><h2>核心业务流程图</h2><p class="muted">业务时序（wiki.yaml flows 手写——维护者补充；系统调用链见下方「系统流程」）。</p>`)
	for _, f := range cfg.Flows {
		b.WriteString("<h3>" + htmlEsc(f.Title) + "</h3>")
		b.WriteString(rc.diagramHTML(f.Mermaid))
	}
	b.WriteString("</section>")
	return b.String()
}
