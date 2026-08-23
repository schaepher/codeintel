package cli

// #241 wiki HTML 输出：单文件自包含 index.html——左侧目录导航（模块
// 可折叠展开，锚点定位）+ 内容区块可折叠 + 内嵌 CSS/JS。双击即用，
// 零部署零依赖；与 md 输出共用 wiki.yaml 契约与六区块数据。

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// renderWikiHTML 生成单文件自包含 index.html（全量覆盖）。
func renderWikiHTML(repoAbs, outDir string, data []*domain.WikiModule, cfg wikiConfig, cols []*domain.TableColumn) error {
	logger := zap.L()
	logger.Debug("enter renderWikiHTML", zap.Int("modules", len(data)))
	defer logger.Debug("exit renderWikiHTML")
	if err := os.RemoveAll(outDir); err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	meta, tableAlias, hidden := wikiMetaIndex(cfg)

	// 模块排序（order 优先）
	ordered := append([]*domain.WikiModule(nil), data...)
	sort.SliceStable(ordered, func(i, j int) bool {
		oi, oj := meta[ordered[i].Name].order, meta[ordered[j].Name].order
		if oi != oj {
			return oi != 0 && (oj == 0 || oi < oj)
		}
		return ordered[i].Name < ordered[j].Name
	})

	title := filepath.Base(repoAbs) + " 业务 wiki"
	var nav strings.Builder   // 左侧目录
	var main strings.Builder  // 内容区
	for i, wm := range ordered {
		secID := fmt.Sprintf("sec-%d", i)
		modID := fmt.Sprintf("mod-%d", i)
		desc := meta[wm.Name].desc
		label := wm.Name
		if desc != "" {
			label += " — " + desc
		}
		// 目录：模块条目（可折叠 → 章节锚点）
		nav.WriteString(fmt.Sprintf(
			`<li class="mod"><div class="mod-head fold-btn" data-target="%s" data-label="1">▸ %s</div><ul class="mod-sec" id="%s">`,
			secID, htmlEsc(label), secID))
		for _, a := range moduleAnchors(wm) {
			nav.WriteString(fmt.Sprintf(`<li><a href="#%s-%d">%s</a></li>`, a.key, i, a.label))
		}
		nav.WriteString("</ul></li>\n")

		// 内容：模块 section
		main.WriteString(fmt.Sprintf(`<section id="%s"><h2>%s</h2>`, modID, htmlEsc(wm.Name)))
		if desc != "" {
			main.WriteString("<blockquote>" + htmlEsc(desc) + "</blockquote>")
		}
		main.WriteString(renderModuleHTML(wm, i, tableAlias, hidden, cfg))
		main.WriteString("</section>\n")
	}
	// 表清单 section + 每表详情（字段定义/索引/建表语句，#243）
	tableCfgs := tableCfgsFrom(cfg)
	tables := collectTables(data, tableAlias)
	main.WriteString(`<section id="tables"><h2>表清单</h2>`)
	if len(tables) == 0 {
		main.WriteString("<p>（未识别到 ORM 表写入）</p>")
	} else {
		main.WriteString("<table><tr><th>表</th><th>别名</th><th>涉及模块</th></tr>")
		for _, t := range tables {
			main.WriteString(fmt.Sprintf("<tr><td><a href=\"#tbl-%s\">%s</a></td><td>%s</td><td>%s</td></tr>",
				htmlEsc(t.name), htmlEsc(t.name), htmlEsc(t.alias), htmlEsc(strings.Join(t.mods, ", "))))
		}
		main.WriteString("</table>")
		// 每表详情小节
		for _, t := range tables {
			main.WriteString(fmt.Sprintf(`<h3 id="tbl-%s">%s</h3>`, htmlEsc(t.name), htmlEsc(t.name)))
			if t.alias != "" {
				main.WriteString("<blockquote>" + htmlEsc(t.alias) + "</blockquote>")
			}
			tc := tableCfgs[t.name]
			rows := mergeTableColumns(t.name, cols, tc.Columns)
			if len(rows) == 0 {
				main.WriteString("<p class=\"muted\">（无字段信息——维护者可在 wiki.yaml tables.columns 补充）</p>")
			} else {
				main.WriteString("<table><tr><th>字段名</th><th>类型</th><th>默认值</th><th>说明</th></tr>")
				for _, c := range rows {
					main.WriteString(fmt.Sprintf("<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>",
						htmlEsc(c.name), htmlEsc(c.typ), htmlEsc(c.def), htmlEsc(c.comment)))
				}
				main.WriteString("</table>")
			}
			if len(tc.Indexes) > 0 {
				main.WriteString("<h4>索引</h4>")
				for _, ix := range tc.Indexes {
					main.WriteString("<p><code>" + htmlEsc(ix) + "</code></p>")
				}
			}
			if tc.DDL != "" {
				main.WriteString("<h4>建表语句</h4><pre><code>" + htmlEsc(tc.DDL) + "</code></pre>")
			}
		}
	}
	main.WriteString("</section>\n")
	nav.WriteString(`<li><a href="#tables">表清单</a></li>`)

	html := wikiHTMLPage(title, cfg.Project.Description, nav.String(), main.String())
	return os.WriteFile(filepath.Join(outDir, "index.html"), []byte(html), 0o644)
}

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
func renderModuleHTML(wm *domain.WikiModule, i int, tableAlias map[string]string, hidden map[string]bool, cfg wikiConfig) string {
	var b strings.Builder
	sec := func(key, title string) string {
		return fmt.Sprintf(`<h3 class="fold-btn" data-target="%s-%d" data-label="1">▾ %s</h3><div class="sec-body" id="%s-%d">`,
			key, i, title, key, i)
	}
	// 职责
	b.WriteString(sec("desc", "职责"))
	if wm.Desc != "" {
		b.WriteString("<p>" + htmlEsc(wm.Desc) + "</p>")
	} else {
		b.WriteString("<p class=\"muted\">（无包注释——维护者可在 wiki.yaml 补充）</p>")
	}
	b.WriteString("</div>\n")
	// 入口
	if len(wm.Entries) > 0 {
		b.WriteString(sec("entry", "入口"))
		for _, e := range wm.Entries {
			b.WriteString("<p><code>" + htmlEsc(e) + "</code></p>")
		}
		b.WriteString("</div>\n")
	}
	// 核心符号
	b.WriteString(sec("core", "核心符号（被调用最多）"))
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
	// 调用的模块 / 被哪些模块调用
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
	// 相关表
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
	// 架构图（#241）：yaml 覆盖优先，否则自动模块间调用图
	b.WriteString(sec("arch", "架构图"))
	arch := ""
	if cfg.Architecture != "" {
		arch = cfg.Architecture
	} else {
		arch = moduleArchMermaid(wm)
	}
	if arch != "" {
		b.WriteString("<pre class=\"mermaid\">" + htmlEsc(arch) + "</pre>")
	} else {
		b.WriteString("<p class=\"muted\">（单模块或无线索——yaml architecture 可手写）</p>")
	}
	b.WriteString("</div>\n")
	// 流程时序（#242）：yaml 业务时序各自单独 + 自动时序每个一级调用
	// 分支单独一张图
	b.WriteString(sec("seq", "流程时序"))
	hasSeq := false
	for _, f := range cfg.Flows {
		b.WriteString("<h4>" + htmlEsc(f.Title) + "</h4>")
		b.WriteString("<pre class=\"mermaid\">" + htmlEsc(f.Mermaid) + "</pre>")
		hasSeq = true
	}
	for _, fl := range wm.Flows {
		b.WriteString("<h4>" + htmlEsc(fl.Title) + "</h4>")
		b.WriteString("<pre class=\"mermaid\">" + htmlEsc(sequenceMermaid(fl.Steps)) + "</pre>")
		hasSeq = true
	}
	if !hasSeq {
		b.WriteString("<p class=\"muted\">（无调用链——yaml flows 可手写业务时序）</p>")
	}
	b.WriteString("</div>\n")
	return b.String()
}

// tableRow 表清单行。
type tableRow struct {
	name  string
	alias string
	mods  []string
}

// collectTables 表清单（去重 + 涉及模块）。
func collectTables(data []*domain.WikiModule, tableAlias map[string]string) []tableRow {
	byName := map[string]*tableRow{}
	var order []string
	for _, wm := range data {
		for _, t := range wm.Tables {
			r, ok := byName[t]
			if !ok {
				r = &tableRow{name: t, alias: tableAlias[t]}
				byName[t] = r
				order = append(order, t)
			}
			if !containsStr(r.mods, wm.ShortName) {
				r.mods = append(r.mods, wm.ShortName)
			}
		}
	}
	sort.Strings(order)
	out := make([]tableRow, 0, len(order))
	for _, t := range order {
		out = append(out, *byName[t])
	}
	return out
}

// wikiHTMLPage 组装完整页面（内嵌 CSS/JS）。
func wikiHTMLPage(title, desc, nav, main string) string {
	return `<!DOCTYPE html>
<html lang="zh">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>` + htmlEsc(title) + `</title>
<style>
* { box-sizing: border-box; margin: 0; padding: 0; }
body { font-family: -apple-system, "PingFang SC", "Microsoft YaHei", sans-serif; color: #1f2329; }
#sidebar { position: fixed; left: 0; top: 0; bottom: 0; width: 280px; overflow-y: auto;
  background: #f6f8fa; border-right: 1px solid #e5e6eb; padding: 16px 0; }
#sidebar h2 { font-size: 14px; padding: 0 16px 8px; color: #86909c; }
#sidebar ul { list-style: none; }
#sidebar .mod-head { padding: 8px 16px; cursor: pointer; font-weight: 600; font-size: 13px; }
#sidebar .mod-head:hover { background: #e5e6eb; }
#sidebar .mod-sec { padding: 0 0 6px 24px; font-size: 12px; }
#sidebar .mod-sec a { display: block; padding: 3px 8px; color: #4e5969; text-decoration: none; border-radius: 4px; }
#sidebar .mod-sec a:hover { background: #e5e6eb; color: #1677ff; }
#sidebar > ul > li > a { display: block; padding: 6px 16px; font-size: 13px; color: #4e5969; text-decoration: none; }
#sidebar > ul > li > a:hover { color: #1677ff; }
#main { margin-left: 280px; padding: 24px 40px 60px; max-width: 960px; }
#main h2 { font-size: 22px; margin: 8px 0 12px; border-bottom: 1px solid #e5e6eb; padding-bottom: 8px; }
#main h3 { font-size: 15px; margin: 20px 0 8px; cursor: pointer; user-select: none; color: #1d2129; }
#main h3:hover { color: #1677ff; }
#main blockquote { margin: 8px 0 16px; padding: 8px 12px; background: #f2f3f5; border-left: 3px solid #1677ff; color: #4e5969; }
#main p { margin: 6px 0; font-size: 14px; line-height: 1.7; }
#main code { background: #f2f3f5; padding: 1px 6px; border-radius: 4px; font-size: 13px; }
#main table { border-collapse: collapse; margin: 8px 0 16px; width: 100%; font-size: 13px; }
#main th, #main td { border: 1px solid #e5e6eb; padding: 6px 10px; text-align: left; }
#main th { background: #f7f8fa; }
#main .muted { color: #86909c; }
#main section { margin-bottom: 32px; }
.fold-btn { cursor: pointer; }
</style>
</head>
<body>
<div id="sidebar">
<h2>目录</h2>
<ul id="nav">` + nav + `</ul>
</div>
<div id="main">
<h1>` + htmlEsc(title) + `</h1>
` + (func() string {
		if desc != "" {
			return "<blockquote>" + htmlEsc(desc) + "</blockquote>\n"
		}
		return ""
	}()) + main + `
</div>
<script src="https://cdn.jsdelivr.net/npm/mermaid@10/dist/mermaid.min.js"></script>
<script>
mermaid.initialize({ startOnLoad: true, theme: 'neutral' });
document.addEventListener('click', function (ev) {
  var btn = ev.target.closest('.fold-btn');
document.addEventListener('click', function (ev) {
  var btn = ev.target.closest('.fold-btn');
  if (!btn) return;
  var target = document.getElementById(btn.getAttribute('data-target'));
  if (!target) return;
  var collapsed = target.style.display === 'none';
  target.style.display = collapsed ? '' : 'none';
  if (btn.getAttribute('data-label') === '1') {
    btn.textContent = (collapsed ? '▾ ' : '▸ ') + btn.textContent.replace(/^[▾▸] /, '');
  }
});
</script>
</body>
</html>
`
}

// htmlEsc HTML 转义。
func htmlEsc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")
	return r.Replace(s)
}

// containsStr 字符串切片包含判断（cli 包本地版）。
func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
