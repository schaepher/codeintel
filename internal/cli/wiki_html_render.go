package cli

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
func renderWikiHTML(repoAbs, outDir string, data []*domain.WikiModule, cfg wikiConfig, cols []*domain.TableColumn, rels []*domain.TableRelation) error {
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

	ordered := append([]*domain.WikiModule(nil), data...)
	sort.SliceStable(ordered, func(i, j int) bool {
		oi, oj := meta[ordered[i].Name].order, meta[ordered[j].Name].order
		if oi != oj {
			return oi != 0 && (oj == 0 || oi < oj)
		}
		return ordered[i].Name < ordered[j].Name
	})

	title := filepath.Base(repoAbs) + " 业务 wiki"
	var nav strings.Builder  // 左侧目录
	var main strings.Builder // 内容区
	for i, wm := range ordered {
		secID := fmt.Sprintf("sec-%d", i)
		modID := fmt.Sprintf("mod-%d", i)
		desc := meta[wm.Name].desc
		label := wm.Name
		if desc != "" {
			label += " — " + desc
		}

		nav.WriteString(fmt.Sprintf(
			`<li class="mod"><div class="mod-head fold-btn" data-target="%s" data-label="1">▸ %s</div><ul class="mod-sec" id="%s">`,
			secID, htmlEsc(label), secID))
		for _, a := range moduleAnchors(wm) {
			nav.WriteString(fmt.Sprintf(`<li><a href="#%s-%d">%s</a></li>`, a.key, i, a.label))
		}
		nav.WriteString("</ul></li>\n")

		main.WriteString(fmt.Sprintf(`<section id="%s"><h2>%s</h2>`, modID, htmlEsc(wm.Name)))
		if desc != "" {
			main.WriteString("<blockquote>" + htmlEsc(desc) + "</blockquote>")
		}
		main.WriteString(renderModuleHTML(wm, i, tableAlias, hidden, cfg, desc))
		main.WriteString("</section>\n")
	}

	if cfg.Architecture != "" {
		main.WriteString(`<section id="arch"><h2>架构图</h2><pre class="mermaid">` + htmlEsc(cfg.Architecture) + `</pre></section>` + "\n")
		nav.WriteString(`<li><a href="#arch">架构图</a></li>`)
	}

	hideTable := map[string]bool{}
	for _, t := range cfg.Tables {
		if t.Hidden {
			hideTable[t.Name] = true
		}
	}
	erMermaid := renderERMermaid(rels, hideTable)
	main.WriteString(`<section id="er"><h2>ER 图（表间关系）</h2>`)
	if strings.Contains(erMermaid, "||--") {
		main.WriteString(`<p class="muted">表间直接键关联（fk=值流验证的真实键 / query=WHERE 键关联），列级标注。字段定义见下方表清单。</p><pre class="mermaid">` + htmlEsc(erMermaid) + `</pre>`)
	} else {
		main.WriteString("<p class=\"muted\">（无表间直接关联）</p>")
	}
	main.WriteString("</section>\n")
	nav.WriteString(`<li><a href="#er">ER 图</a></li>`)

	tableCfgs := tableCfgsFrom(cfg)
	tables := collectTables(data, tableAlias, tableCfgs)
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

	if len(cfg.Glossary) > 0 {
		main.WriteString(`<section id="glossary"><h2>术语表</h2>`)
		for _, g := range cfg.Glossary {
			main.WriteString(fmt.Sprintf("<p><strong>%s</strong>：%s</p>", htmlEsc(g.Term), htmlEsc(g.Definition)))
		}
		main.WriteString("</section>\n")
		nav.WriteString(`<li><a href="#glossary">术语表</a></li>`)
	}

	html := wikiHTMLPage(title, cfg.Project.Description, nav.String(), main.String())
	return os.WriteFile(filepath.Join(outDir, "index.html"), []byte(html), 0o644)
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
