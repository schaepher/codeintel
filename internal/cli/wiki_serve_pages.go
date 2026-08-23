package cli

// P2b wiki serve 页面渲染（wiki_serve.go 按主题拆）：左侧目录 + 各页
// 内容。复用单文件 html 的渲染原语（renderModuleHTML/renderERMermaid/
// collectTables/wikiTablesSectionHTML）与 wikiHTMLPage 模板。

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// wikiGuide 多页版快速开始引导（/wiki/ 路径链接）。
const wikiGuide = `<strong>快速开始：</strong>① 看<a href="/wiki/overview#arch">架构图</a>了解系统组成 → ② 按顺序读各模块（职责 → 入口 → 核心符号 → 相关表）→ ③ 查<a href="/wiki/tables">表清单</a>看字段与建表语句。`

// navHTML 左侧目录（多页版：模块组/概览/ER 图/表清单；current 高亮）。
func (ws *wikiServe) navHTML(snap *wikiSnapshot, current string) string {
	var nav strings.Builder
	nav.WriteString(`<li class="mod"><div class="mod-head fold-btn" data-target="wiki-sec" data-label="1">▸ 模块</div><ul class="mod-sec" id="wiki-sec">`)
	for _, wm := range snap.ordered {
		href := "/wiki/mod/" + wm.ShortName
		cls := ""
		if href == current {
			cls = ` class="active"`
		}
		label := wm.Name
		if d := snap.meta[wm.Name].desc; d != "" {
			label += " — " + d
		}
		nav.WriteString(fmt.Sprintf(`<li><a%s href="%s">%s</a></li>`, cls, href, htmlEsc(label)))
	}
	nav.WriteString("</ul></li>\n")
	for _, item := range []struct{ href, label string }{
		{"/wiki/overview", "概览"},
		{"/wiki/er", "ER 图（表间关系）"},
		{"/wiki/tables", "表清单"},
	} {
		cls := ""
		if item.href == current {
			cls = ` class="active"`
		}
		nav.WriteString(fmt.Sprintf(`<li><a%s href="%s">%s</a></li>`, cls, item.href, htmlEsc(item.label)))
	}
	return nav.String()
}

// pageHTML 组装整页（标题/导航/内容）。
func (ws *wikiServe) pageHTML(snap *wikiSnapshot, current string, main string) string {
	title := filepath.Base(ws.repoAbs) + " 业务 wiki"
	return wikiHTMLPage(title, snap.cfg.Project.Description, wikiGuide, ws.navHTML(snap, current), main)
}

// overviewPage 概览页：架构图 + 模块目录 + 术语表。
func (ws *wikiServe) overviewPage(snap *wikiSnapshot) string {
	var b strings.Builder
	if snap.cfg.Architecture != "" {
		b.WriteString(`<section id="arch"><h2>架构图</h2><pre class="mermaid">` + htmlEsc(snap.cfg.Architecture) + `</pre></section>` + "\n")
	}
	b.WriteString(`<section id="modules"><h2>模块</h2>`)
	if len(snap.ordered) == 0 {
		b.WriteString("<p class=\"muted\">（未识别到模块）</p>")
	} else {
		b.WriteString("<ul>")
		for _, wm := range snap.ordered {
			label := wm.Name
			if d := snap.meta[wm.Name].desc; d != "" {
				label += " — " + d
			}
			b.WriteString(fmt.Sprintf(`<li><a href="/wiki/mod/%s">%s</a></li>`, htmlEsc(wm.ShortName), htmlEsc(label)))
		}
		b.WriteString("</ul>")
	}
	b.WriteString("</section>\n")
	if len(snap.cfg.Glossary) > 0 {
		b.WriteString(`<section id="glossary"><h2>术语表</h2>`)
		for _, g := range snap.cfg.Glossary {
			b.WriteString(fmt.Sprintf("<p><strong>%s</strong>：%s</p>", htmlEsc(g.Term), htmlEsc(g.Definition)))
		}
		b.WriteString("</section>\n")
	}
	return ws.pageHTML(snap, "/wiki/overview", b.String())
}

// modulePage 模块页（六区块内容，与单文件 html 同渲染）。
func (ws *wikiServe) modulePage(snap *wikiSnapshot, wm *domain.WikiModule) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf(`<section id="mod-0"><h2>%s</h2>`, htmlEsc(wm.Name)))
	if d := snap.meta[wm.Name].desc; d != "" {
		b.WriteString("<blockquote>" + htmlEsc(d) + "</blockquote>")
	}
	b.WriteString(renderModuleHTML(wm, 0, snap.tableAlias, snap.hidden, snap.cfg, snap.meta[wm.Name].desc))
	b.WriteString("</section>\n")
	return ws.pageHTML(snap, "/wiki/mod/"+wm.ShortName, b.String())
}

// erPage ER 图页。
func (ws *wikiServe) erPage(snap *wikiSnapshot) string {
	hideTable := map[string]bool{}
	for _, t := range snap.cfg.Tables {
		if t.Hidden {
			hideTable[t.Name] = true
		}
	}
	erMermaid := renderERMermaid(snap.rels, hideTable)
	var b strings.Builder
	b.WriteString(`<section id="er"><h2>ER 图（表间关系）</h2>`)
	if strings.Contains(erMermaid, "||--") {
		b.WriteString(`<p class="muted">表间直接键关联（fk=值流验证的真实键 / query=WHERE 键关联），列级标注。字段定义见<a href="/wiki/tables">表清单</a>。</p><pre class="mermaid">` + htmlEsc(erMermaid) + `</pre>`)
	} else {
		b.WriteString("<p class=\"muted\">（无表间直接关联）</p>")
	}
	b.WriteString("</section>\n")
	return ws.pageHTML(snap, "/wiki/er", b.String())
}

// tablesPage 表清单页。
func (ws *wikiServe) tablesPage(snap *wikiSnapshot) string {
	tables := collectTables(snap.data, snap.tableAlias, snap.tableCfgs)
	main := wikiTablesSectionHTML(tables, snap.tableCfgs, snap.cols)
	return ws.pageHTML(snap, "/wiki/tables", main)
}
