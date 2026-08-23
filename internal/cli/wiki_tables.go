package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// renderTablesPage 表清单 + 每表详情（字段定义表/索引/建表语句，#243）。
func renderTablesPage(data []*domain.WikiModule, tableAlias map[string]string, tableCfgs map[string]wikiTableConfig, cols []*domain.TableColumn) string {
	var b strings.Builder
	b.WriteString("# 表清单\n\n> 自动生成：gorm/xorm 写路径识别；别名与字段说明可在 wiki.yaml tables 补充。\n\n")
	seen := map[string]bool{}
	var tables []string
	for _, wm := range data {
		for _, t := range wm.Tables {
			if !seen[t] {
				seen[t] = true
				tables = append(tables, t)
			}
		}
	}

	for name := range tableCfgs {
		if !seen[name] {
			seen[name] = true
			tables = append(tables, name)
		}
	}
	sort.Strings(tables)
	if len(tables) == 0 {
		b.WriteString("（未识别到 ORM 表写入）\n")
		return b.String()
	}
	b.WriteString("| 表 | 别名 | 涉及模块 |\n|---|---|---|\n")
	for _, t := range tables {
		alias := tableAlias[t]
		var mods []string
		for _, wm := range data {
			for _, wt := range wm.Tables {
				if wt == t {
					mods = append(mods, wm.ShortName)
					break
				}
			}
		}
		b.WriteString(fmt.Sprintf("| [%s](#%s) | %s | %s |\n", t, t, alias, strings.Join(mods, ", ")))
	}

	b.WriteString("\n---\n\n")
	for _, t := range tables {
		b.WriteString(fmt.Sprintf("## %s\n\n", t))
		if alias := tableAlias[t]; alias != "" {
			b.WriteString("> " + alias + "\n\n")
		}
		cfg := tableCfgs[t]
		rows := mergeTableColumns(t, cols, cfg.Columns)
		if len(rows) == 0 {
			b.WriteString("（无字段信息——维护者可在 wiki.yaml tables.columns 补充）\n\n")
		} else {
			b.WriteString("### 字段\n\n| 字段名 | 类型 | 默认值 | 说明 |\n|---|---|---|---|\n")
			for _, c := range rows {
				b.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n", c.name, c.typ, c.def, c.comment))
			}
			b.WriteString("\n")
		}
		if len(cfg.Indexes) > 0 {
			b.WriteString("### 索引\n\n")
			for _, ix := range cfg.Indexes {
				b.WriteString("- `" + ix + "`\n")
			}
			b.WriteString("\n")
		}
		if cfg.DDL != "" {
			b.WriteString("### 建表语句\n\n```sql\n" + cfg.DDL + "\n```\n\n")
		}
	}
	return b.String()
}

// wikiGapReport 描述补全缺口统计（D）：无描述模块数、无别名表数、
// 无 comment 表列数——生成/浏览时提示用户补 wiki.yaml。
func wikiGapReport(data []*domain.WikiModule, cfg wikiConfig, cols []*domain.TableColumn) (modsNoDesc, tablesNoAlias, colsNoComment int) {
	meta, tableAlias, _ := wikiMetaIndex(cfg)
	for _, wm := range data {
		if meta[wm.Name].desc == "" && wm.Desc == "" {
			modsNoDesc++
		}
	}
	tableCfgs := tableCfgsFrom(cfg)
	for _, t := range collectTables(data, tableAlias, tableCfgs) {
		if t.alias == "" {
			tablesNoAlias++
		}
		tc := tableCfgs[t.name]
		for _, r := range mergeTableColumns(t.name, cols, tc.Columns) {
			if r.comment == "" {
				colsNoComment++
			}
		}
	}
	return
}

// wikiMetaIndex 从 yaml 构建渲染索引（模块描述/顺序、表别名、隐藏符号）。
func wikiMetaIndex(cfg wikiConfig) (map[string]wikiMeta, map[string]string, map[string]bool) {
	meta := map[string]wikiMeta{}
	tableAlias := map[string]string{}
	hidden := map[string]bool{}
	for _, m := range cfg.Modules {
		meta[m.Name] = wikiMeta{desc: m.Description, order: m.Order}
	}
	for _, t := range cfg.Tables {
		tableAlias[t.Name] = t.Alias
	}
	for _, s := range cfg.HiddenSymbols {
		hidden[s] = true
	}
	return meta, tableAlias, hidden
}

// tableColRow 渲染用表字段行。
type tableColRow struct {
	name    string
	typ     string
	def     string
	comment string
}

// mergeTableColumns 表字段合并（#243 自动初稿 + yaml 覆盖）：
// 自动列（ER 表列虚拟节点：列名 + gorm tag 类型）为底，yaml columns
// 覆盖同名（type/default/comment 各自覆盖），自动列未列出的补全。
func mergeTableColumns(table string, cols []*domain.TableColumn, yamlCols []struct {
	Name    string `yaml:"name"`
	Type    string `yaml:"type"`
	Default string `yaml:"default"`
	Comment string `yaml:"comment"`
}) []tableColRow {

	byName := map[string]tableColRow{}
	var order []string
	for _, c := range yamlCols {
		byName[c.Name] = tableColRow{name: c.Name, typ: c.Type, def: c.Default, comment: c.Comment}
		order = append(order, c.Name)
	}

	prefix := table + "."
	for _, c := range cols {
		if !strings.HasPrefix(c.Name, prefix) {
			continue
		}
		col := strings.TrimPrefix(c.Name, prefix)
		if r, ok := byName[col]; ok {
			if r.typ == "" {
				r.typ = c.ColType
			}
			byName[col] = r
			continue
		}
		byName[col] = tableColRow{name: col, typ: c.ColType}
		order = append(order, col)
	}
	out := make([]tableColRow, 0, len(order))
	for _, n := range order {
		out = append(out, byName[n])
	}
	return out
}
