package cli

import (
	"fmt"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// moduleAnchor 模块章节锚点。
type moduleAnchor struct {
	key   string
	label string
}

// moduleAnchors 模块页章节锚点（按存在性）。
func moduleAnchors(wm *domain.WikiModule) []moduleAnchor {
	var out []moduleAnchor
	out = append(out, moduleAnchor{"desc", "职责"})
	if len(wm.Entries) > 0 {
		out = append(out, moduleAnchor{"entry", "入口"})
	}
	out = append(out, moduleAnchor{"core", "核心符号"})
	if len(wm.OutCalls) > 0 {
		out = append(out, moduleAnchor{"out", "调用的模块"})
	}
	if len(wm.InCalls) > 0 {
		out = append(out, moduleAnchor{"in", "被哪些模块调用"})
	}
	if len(wm.Tables) > 0 {
		out = append(out, moduleAnchor{"tbl", "相关表"})
	}
	return out
}

// renderModuleHTML 模块内容（区块标题可折叠，默认展开）。
func renderModuleHTML(wm *domain.WikiModule, i int, tableAlias map[string]string, hidden map[string]bool, cfg wikiConfig, desc string) string {
	var b strings.Builder
	sec := func(key, title string) string {
		return fmt.Sprintf(`<h3 class="fold-btn" data-target="%s-%d" data-label="1">▾ %s</h3><div class="sec-body" id="%s-%d">`,
			key, i, title, key, i)
	}

	b.WriteString(sec("desc", "职责"))
	if desc != "" {
		b.WriteString("<p>" + htmlEsc(desc) + "</p>")
	}
	if wm.Desc != "" {
		b.WriteString("<p>" + htmlEsc(wm.Desc) + "</p>")
	}
	if desc == "" && wm.Desc == "" {
		b.WriteString("<p class=\"muted\">（无描述——维护者可在 wiki.yaml modules.description 补充）</p>")
	}
	b.WriteString("</div>\n")

	if len(wm.Entries) > 0 {
		b.WriteString(sec("entry", "入口"))
		for _, e := range wm.Entries {
			b.WriteString("<p><code>" + htmlEsc(e) + "</code></p>")
		}
		b.WriteString("</div>\n")
	}

	b.WriteString(sec("core", "核心符号（内部实现参考——被调用最多）"))
	if len(wm.CoreSymbols) > 0 {
		b.WriteString("<table><tr><th>符号</th><th>类型</th><th>调用者数</th><th>位置</th></tr>")
		for _, s := range wm.CoreSymbols {
			if hidden[s.Name] {
				continue
			}
			loc := ""
			if s.File != "" {
				loc = fmt.Sprintf("%s:%d", s.File, s.Line)
			}
			b.WriteString(fmt.Sprintf("<tr><td>%s</td><td>%s</td><td>%d</td><td>%s</td></tr>",
				htmlEsc(s.Name), htmlEsc(s.Kind), s.Callers, htmlEsc(loc)))
		}
		b.WriteString("</table>")
	} else {
		b.WriteString("<p class=\"muted\">（无调用数据）</p>")
	}
	b.WriteString("</div>\n")

	if len(wm.OutCalls) > 0 {
		b.WriteString(sec("out", "调用的模块"))
		for _, m := range wm.OutCalls {
			b.WriteString("<p><code>" + htmlEsc(m) + "</code></p>")
		}
		b.WriteString("</div>\n")
	}
	if len(wm.InCalls) > 0 {
		b.WriteString(sec("in", "被哪些模块调用"))
		for _, m := range wm.InCalls {
			b.WriteString("<p><code>" + htmlEsc(m) + "</code></p>")
		}
		b.WriteString("</div>\n")
	}

	if len(wm.Tables) > 0 {
		b.WriteString(sec("tbl", "相关表"))
		for _, t := range wm.Tables {
			line := "<a href=\"#tbl-" + htmlEsc(t) + "\"><code>" + htmlEsc(t) + "</code></a>"
			if a := tableAlias[t]; a != "" {
				line += "（" + htmlEsc(a) + "）"
			}
			b.WriteString("<p>" + line + "</p>")
		}
		b.WriteString("</div>\n")
	}

	b.WriteString(sec("arch", "架构图（包间调用）"))
	arch := moduleArchMermaid(wm)
	if arch != "" {
		b.WriteString("<pre class=\"mermaid\">" + htmlEsc(arch) + "</pre>")
	} else {
		b.WriteString("<p class=\"muted\">（单模块或无线索；整体架构见页面顶部架构图）</p>")
	}
	b.WriteString("</div>\n")

	b.WriteString(sec("seq", "流程时序"))
	hasSeq := false
	for _, f := range cfg.Flows {
		b.WriteString("<h4>" + htmlEsc(f.Title) + "</h4>")
		b.WriteString("<pre class=\"mermaid\">" + htmlEsc(f.Mermaid) + "</pre>")
		hasSeq = true
	}
	if len(wm.Flows) > 0 {
		b.WriteString("<p class=\"muted\">（自动生成：内部调用链——代码事实；业务时序见上方 yaml flows）</p>")
	}
	for _, fl := range wm.Flows {
		b.WriteString("<h4>内部调用链：" + htmlEsc(fl.Title) + "</h4>")
		b.WriteString("<pre class=\"mermaid\">" + htmlEsc(sequenceMermaid(fl.Steps)) + "</pre>")
		hasSeq = true
	}
	if !hasSeq {
		b.WriteString("<p class=\"muted\">（无调用链——yaml flows 可手写业务时序）</p>")
	}
	b.WriteString("</div>\n")
	return b.String()
}
