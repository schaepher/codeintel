package cli

// P2b wiki serve 页面渲染（wiki_serve.go 按主题拆）：左侧目录 + 各页
// 内容。复用单文件 html 的渲染原语（renderModuleHTML/renderERMermaid/
// collectTables/wikiTablesSectionHTML）与 wikiHTMLPage 模板。

import (
	"encoding/json"
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
		if d := moduleDesc(wm, snap.meta[wm.Name].desc); d != "" {
			label += " — " + d
		}
		nav.WriteString(fmt.Sprintf(`<li><a%s href="%s">%s</a></li>`, cls, href, htmlEsc(label)))
	}
	nav.WriteString("</ul></li>\n")
	for _, item := range []struct{ href, label string }{
		{"/wiki/overview", "概览"},
		{"/wiki/commands", "命令清单"},
		{"/wiki/processes", "系统流程"},
		{"/wiki/api", "HTTP 接口"},
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

// pageHTML 组装整页（标题/导航/内容 + 图探索返回链接 + 新鲜度标注 +
// 跨页搜索索引 + 补全引导横幅）。
func (ws *wikiServe) pageHTML(snap *wikiSnapshot, current string, main string) string {
	title := filepath.Base(ws.repoAbs) + " 业务 wiki"
	freshNote := ""
	if snap.commitSHA != "" {
		freshNote = "索引 commit: " + shortSHA(snap.commitSHA) + "（增量 update 后自动刷新）"
	}
	return wikiHTMLPage(title, snap.cfg.Project.Description, wikiGuide, ws.navHTML(snap, current),
		gapBannerHTML(snap)+main,
		wikiPageOpts{exploreLink: "/", freshNote: freshNote, searchIndex: searchIndexJSON(snap)})
}

// gapBannerHTML 描述补全引导横幅（D）：缺口数 + 一键关闭（localStorage
// 持久化；wiki.yaml 更新后重新显示）。
func gapBannerHTML(snap *wikiSnapshot) string {
	mods, tbls, cc := wikiGapReport(snap.data, snap.cfg, snap.cols)
	if mods+tbls+cc == 0 {
		return ""
	}
	var parts []string
	if mods > 0 {
		parts = append(parts, fmt.Sprintf("%d 个模块无描述", mods))
	}
	if tbls > 0 {
		parts = append(parts, fmt.Sprintf("%d 张表无别名", tbls))
	}
	if cc > 0 {
		parts = append(parts, fmt.Sprintf("%d 个表列无说明", cc))
	}
	return `<div id="gap-banner" class="guide" style="background:#fff7e6;border-left-color:#fa8c16">` +
		"<strong>wiki 内容待补全：</strong>" + strings.Join(parts, "、") +
		`（编辑仓库根 wiki.yaml 后刷新）<a href="#" onclick="document.getElementById('gap-banner').style.display='none';try{localStorage.setItem('codeintel-wiki-gap','1')}catch(e){};return false" style="float:right">知道了</a></div>`
}

// searchIndexJSON 跨页搜索索引（模块/表/术语——前端输入即达，不依赖
// 服务端接口；字段量大多不索引，进表页后可见）。
func searchIndexJSON(snap *wikiSnapshot) string {
	type item struct {
		T string `json:"t"` // 类型：模块/表/术语
		N string `json:"n"` // 名称
		D string `json:"d"` // 描述（可为空）
		H string `json:"h"` // 跳转 href
	}
	var items []item
	for _, wm := range snap.ordered {
		items = append(items, item{"模块", wm.Name, moduleDesc(wm, snap.meta[wm.Name].desc), "/wiki/mod/" + wm.ShortName})
	}
	for _, t := range collectTables(snap.data, snap.tableAlias, snap.tableCfgs) {
		items = append(items, item{"表", t.name, t.alias, "/wiki/tables#tbl-" + t.name})
	}
	for _, g := range snap.cfg.Glossary {
		items = append(items, item{"术语", g.Term, g.Definition, "/wiki/overview#glossary"})
	}
	items = append(items,
		item{"工具", "命令清单", "全部 CLI 命令", "/wiki/commands"},
		item{"工具", "系统流程", "命令入口调用链", "/wiki/processes"},
		item{"工具", "HTTP 接口", "全部 /api 与 /wiki 路由", "/wiki/api"})
	b, err := json.Marshal(items)
	if err != nil {
		return ""
	}
	return string(b)
}

// overviewPage 概览页：架构图 + 模块目录 + 术语表。
func (ws *wikiServe) overviewPage(snap *wikiSnapshot) string {
	var b strings.Builder
	if snap.cfg.Architecture != "" {
		b.WriteString(`<section id="arch"><h2>架构图</h2><p class="muted">（来源：wiki.yaml architecture）</p><pre class="mermaid">` + htmlEsc(snap.cfg.Architecture) + `</pre></section>` + "\n")
	}
	b.WriteString(`<section id="modules"><h2>模块</h2>`)
	if len(snap.ordered) == 0 {
		b.WriteString("<p class=\"muted\">（未识别到模块）</p>")
	} else {
		b.WriteString("<ul>")
		for _, wm := range snap.ordered {
			label := wm.Name
			if d := moduleDesc(wm, snap.meta[wm.Name].desc); d != "" {
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

// erPage ER 图页（E：?mod=<短名> 按模块过滤——模块表 + 直接关联表；
// 工具条提供模块筛选链接与 serve 交互 ER 图入口）。
func (ws *wikiServe) erPage(snap *wikiSnapshot, mod string) string {
	hideTable := map[string]bool{}
	for _, t := range snap.cfg.Tables {
		if t.Hidden {
			hideTable[t.Name] = true
		}
	}
	rels := snap.rels
	var filterNote string
	if mod != "" {
		rels, filterNote = erRelsForModule(snap, mod, rels, hideTable)
	}
	erMermaid := renderERMermaid(rels, hideTable)
	var b strings.Builder
	b.WriteString(`<section id="er"><h2>ER 图（表间关系）</h2>`)
	// 模块筛选工具条
	b.WriteString(`<p class="muted" style="margin-bottom:4px">按模块筛选：<a href="/wiki/er">全部</a>`)
	for _, wm := range snap.ordered {
		cls := ""
		if wm.ShortName == mod {
			cls = ` style="font-weight:600;color:#1677ff"`
		}
		b.WriteString(fmt.Sprintf(` <a href="/wiki/er?mod=%s"%s>%s</a>`, htmlEsc(wm.ShortName), cls, htmlEsc(shortMod(wm.Name))))
	}
	b.WriteString(`　<a href="/er.html" style="color:#1677ff">交互 ER 图 →</a></p>`)
	if filterNote != "" {
		b.WriteString(`<p class="muted" style="margin-bottom:4px">` + filterNote + `</p>`)
	}
	if strings.Contains(erMermaid, "||--") {
		b.WriteString(`<p class="muted">表间直接键关联（fk=值流验证的真实键 / query=WHERE 键关联），列级标注。字段定义见<a href="/wiki/tables">表清单</a>。</p><pre class="mermaid">` + htmlEsc(erMermaid) + `</pre>`)
	} else {
		b.WriteString("<p class=\"muted\">（无表间直接关联）</p>")
	}
	b.WriteString("</section>\n")
	return ws.pageHTML(snap, "/wiki/er", b.String())
}

// erRelsForModule 模块过滤：模块表 + 直接关联表（关系任一端的表），
// 关系线只保留两端都在集合内的。
func erRelsForModule(snap *wikiSnapshot, mod string, rels []*domain.TableRelation, hideTable map[string]bool) ([]*domain.TableRelation, string) {
	var wm *domain.WikiModule
	for _, m := range snap.ordered {
		if m.ShortName == mod {
			wm = m
			break
		}
	}
	if wm == nil || len(wm.Tables) == 0 {
		return nil, "（该模块无相关表）"
	}
	allow := map[string]bool{}
	for _, t := range wm.Tables {
		allow[t] = true
	}
	// 直接关联表并入（两端都在集合内才保留线——先扩集合再过滤）
	for _, r := range rels {
		if allow[r.FromTable] {
			allow[r.ToTable] = true
		}
		if allow[r.ToTable] {
			allow[r.FromTable] = true
		}
	}
	var out []*domain.TableRelation
	for _, r := range rels {
		if allow[r.FromTable] && allow[r.ToTable] && !hideTable[r.FromTable] && !hideTable[r.ToTable] {
			out = append(out, r)
		}
	}
	return out, fmt.Sprintf("模块 %s 相关表（%d 张，含直接关联表）", shortMod(wm.Name), len(allow))
}

// tablesPage 表清单页。
func (ws *wikiServe) tablesPage(snap *wikiSnapshot) string {
	tables := collectTables(snap.data, snap.tableAlias, snap.tableCfgs)
	main := wikiTablesSectionHTML(tables, snap.tableCfgs, snap.cols)
	return ws.pageHTML(snap, "/wiki/tables", main)
}
