package cli

// R32 图块渲染（从 wiki.go 拆出——行数治理）。

import "encoding/base64"

// diagramMD md 图块：plantuml 模式输出 ```plantuml 文本（md 不嵌 PNG）；
// mermaid 模式原样代码块（R33 方案 A：超 500 边降级提示——浏览器渲染挂）。
func (rc *wikiRenderCtx) diagramMD(mermaid string) string {
	if rc.Diagram != "plantuml" {
		if n := diagramEdgeCount(mermaid); n > mermaidEdgeLimit {
			return "（图过大：" + itoa(n) + " 条边，mermaid 上限 " + itoa(mermaidEdgeLimit) + "——用 `query relations` 按表查询）\n\n"
		}
		return "```mermaid\n" + mermaid + "\n```\n\n"
	}
	return "```plantuml\n" + mermaidToPlantuml(mermaid) + "\n```\n\n"
}

// diagramHTML html 图块：plantuml 模式渲染 PNG → base64 <img>（单文件
// 自包含；渲染失败降级 plantuml 文本块）；mermaid 模式 <pre class="mermaid">
// （R33 方案 A：超 500 边自动降级——尝试 plantuml PNG，失败给提示）。
func (rc *wikiRenderCtx) diagramHTML(mermaid string) string {
	if rc.Diagram != "plantuml" {
		if n := diagramEdgeCount(mermaid); n > mermaidEdgeLimit {
			if puml := mermaidToPlantuml(mermaid); puml != "" {
				if png, err := plantumlRender(puml); err == nil {
					return "<img src=\"data:image/png;base64," + base64.StdEncoding.EncodeToString(png) + "\" alt=\"diagram\"/>"
				}
			}
			return "<p class=\"muted\">图过大（" + itoa(n) + " 条边，mermaid 上限 " + itoa(mermaidEdgeLimit) + "）——用 `query relations` 按表查询</p>"
		}
		return "<pre class=\"mermaid\">" + htmlEsc(mermaid) + "</pre>"
	}
	puml := mermaidToPlantuml(mermaid)
	if puml == "" {
		return "<pre class=\"mermaid\">" + htmlEsc(mermaid) + "</pre>"
	}
	// R33：plantuml 渲染大图也慢/失败（go2o 1283 边 ER 图实测）——
	// 超限直接提示，不白等渲染
	if n := diagramEdgeCount(mermaid); n > mermaidEdgeLimit {
		return "<p class=\"muted\">图过大（" + itoa(n) + " 条边）——按领域分组图或 `query relations` 按表查询</p>"
	}
	if png, err := plantumlRender(puml); err == nil {
		return "<img src=\"data:image/png;base64," + base64.StdEncoding.EncodeToString(png) + "\" alt=\"diagram\"/>"
	}
	return "<pre class=\"plantuml\">" + htmlEsc(puml) + "</pre>"
}
