package cli

// #241 wiki HTML 输出：单文件自包含 index.html——左侧目录导航（模块
// 可折叠展开，锚点定位）+ 内容区块可折叠 + 内嵌 CSS/JS。双击即用，
// 零部署零依赖；与 md 输出共用 wiki.yaml 契约与六区块数据。

import (
	"fmt"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)





// tableRow 表清单行。
type tableRow struct {
	name  string
	alias string
	mods  []string
}

// collectTables 表清单（去重 + 涉及模块）。
func collectTables(data []*domain.WikiModule, tableAlias map[string]string, tableCfgs map[string]wikiTableConfig) []tableRow {
	byName := map[string]*tableRow{}
	var order []string
	add := func(t string, short string) {
		r, ok := byName[t]
		if !ok {
			r = &tableRow{name: t, alias: tableAlias[t]}
			byName[t] = r
			order = append(order, t)
		}
		if short != "" && !containsStr(r.mods, short) {
			r.mods = append(r.mods, short)
		}
	}
	for _, wm := range data {
		for _, t := range wm.Tables {
			add(t, wm.ShortName)
		}
	}
	// #249：yaml 手写定义的表也渲染
	for name := range tableCfgs {
		add(name, "")
	}
	sort.Strings(order)
	out := make([]tableRow, 0, len(order))
	for _, t := range order {
		out = append(out, *byName[t])
	}
	return out
}

// wikiTablesSectionHTML 表清单 section（单文件 html 与 wiki serve 网页版
// 共用）：表索引表格 + 每表字段/索引/DDL。
func wikiTablesSectionHTML(tables []tableRow, tableCfgs map[string]wikiTableConfig, cols []*domain.TableColumn, schemas map[string]map[string]schemaCol, ormStructs map[string][]ormStruct, goTypes map[string]map[string]string) string {
	var b strings.Builder
	b.WriteString(`<section id="tables"><h2>表清单</h2>`)
	b.WriteString(`<p class="muted">自动生成：gorm/xorm 写路径识别；别名与字段说明可在 wiki.yaml tables 补充。</p>`)
	if len(tables) == 0 {
		b.WriteString("<p>（未识别到 ORM 表写入）</p>")
	} else {
		b.WriteString("<table><tr><th>表</th><th>别名</th><th>涉及模块</th></tr>")
		for _, t := range tables {
			b.WriteString(fmt.Sprintf("<tr><td><a href=\"#tbl-%s\">%s</a></td><td>%s</td><td>%s</td></tr>",
				htmlEsc(t.name), htmlEsc(t.name), htmlEsc(t.alias), htmlEsc(strings.Join(t.mods, ", "))))
		}
		b.WriteString("</table>")

		for _, t := range tables {
			b.WriteString(fmt.Sprintf(`<h3 id="tbl-%s">%s</h3>`, htmlEsc(t.name), htmlEsc(t.name)))
			if t.alias != "" {
				b.WriteString("<blockquote>" + htmlEsc(t.alias) + "</blockquote>")
			}
			tc := tableCfgs[t.name]
			// R20：表上方关联结构体（可折叠核对）
			if sec := renderORMStructSectionHTML(t.name, ormStructs[t.name]); sec != "" {
				b.WriteString(sec)
			}
			rows := mergeTableColumnsWithSchema(t.name, cols, tc.Columns, schemas, goTypes)
			if len(rows) == 0 {
				b.WriteString("<p class=\"muted\">（无字段信息——维护者可在 wiki.yaml tables.columns 补充）</p>")
			} else {
				b.WriteString("<table><tr><th>字段名</th><th>类型</th><th>默认值</th><th>说明</th></tr>")
				for _, c := range rows {
					b.WriteString(fmt.Sprintf("<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>",
						htmlEsc(c.name), htmlEsc(c.typ), htmlEsc(c.def), htmlEsc(c.comment)))
				}
				b.WriteString("</table>")
			}
			if len(tc.Indexes) > 0 {
				b.WriteString("<h4>索引</h4>")
				for _, ix := range tc.Indexes {
					b.WriteString("<p><code>" + htmlEsc(ix) + "</code></p>")
				}
			}
			if tc.DDL != "" {
				b.WriteString("<h4>建表语句</h4><pre><code>" + htmlEsc(tc.DDL) + "</code></pre>")
			}
		}
	}
	b.WriteString("</section>\n")
	return b.String()
}



