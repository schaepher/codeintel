package cli

// R32 图块渲染（从 wiki.go 拆出——行数治理）。

import (
	"encoding/base64"
	"fmt"
)

// plantumlRenderFunc 可注入（测试替换——R99 失败即停验证）。
var plantumlRenderFunc = plantumlRender

// diagramMD md 图块：plantuml 模式渲染 PNG → base64 <img> 嵌入
// （R83：凡是用 plantuml 的都要先转成图片再放进去——md 也看图，
// 不再只给文本代码块；渲染失败降级文本块）；mermaid 模式原样代码块
// （R33 方案 A：超 500 边降级提示——浏览器渲染挂）。
func (rc *wikiRenderCtx) diagramMD(mermaid string) string {
	if rc.Diagram != "plantuml" {
		if n := diagramEdgeCount(mermaid); n > mermaidEdgeLimit {
			return "（图过大：" + itoa(n) + " 条边，mermaid 上限 " + itoa(mermaidEdgeLimit) + "——用 `query relations` 按表查询）\n\n"
		}
		return "```mermaid\n" + mermaid + "\n```\n\n"
	}
	// R83：plantuml 一律渲染 PNG 嵌入（md 支持 <img>）；超限直接提示
	if n := diagramEdgeCount(mermaid); n > mermaidEdgeLimit {
		return "（图过大：" + itoa(n) + " 条边——用 `query relations` 按表查询）\n\n"
	}
	puml := mermaidToPlantuml(mermaid)
	if puml == "" {
		// R99（用户）：mermaid→plantuml 转换失败同样立即停止并报错
		if rc.renderErr == nil {
			rc.renderErr = fmt.Errorf("mermaid→plantuml 转换失败（不支持的图类型）")
		}
		return ""
	}
	png, err := plantumlRenderFunc(puml)
	if err == nil {
		return "<img src=\"data:image/png;base64," + base64.StdEncoding.EncodeToString(png) + "\" alt=\"diagram\"/>\n\n"
	}
	// R99（用户）：plantuml 转换失败立即停止并报错——不降级文本块
	if rc.renderErr == nil {
		rc.renderErr = fmt.Errorf("plantuml 转换失败: %w", err)
	}
	return ""
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
		// R99（用户）：mermaid→plantuml 转换失败同样立即停止并报错
		if rc.renderErr == nil {
			rc.renderErr = fmt.Errorf("mermaid→plantuml 转换失败（不支持的图类型）")
		}
		return ""
	}
	// R33：plantuml 渲染大图也慢/失败（go2o 1283 边 ER 图实测）——
	// 超限直接提示，不白等渲染
	if n := diagramEdgeCount(mermaid); n > mermaidEdgeLimit {
		return "<p class=\"muted\">图过大（" + itoa(n) + " 条边）——按领域分组图或 `query relations` 按表查询</p>"
	}
	png, err := plantumlRenderFunc(puml)
	if err == nil {
		return "<img src=\"data:image/png;base64," + base64.StdEncoding.EncodeToString(png) + "\" alt=\"diagram\"/>"
	}
	// R99（用户）：plantuml 转换失败立即停止并报错——不降级文本块
	if rc.renderErr == nil {
		rc.renderErr = fmt.Errorf("plantuml 转换失败: %w", err)
	}
	return ""
}
