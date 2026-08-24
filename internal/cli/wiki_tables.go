package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
)

// renderTablesPage 表清单 + 每表详情（字段定义表/索引/建表语句，#243）。
func renderTablesPage(data []*domain.WikiModule, tableAlias map[string]string, tableCfgs map[string]wikiTableConfig, cols []*domain.TableColumn, schemas map[string]map[string]schemaCol, ormStructs map[string][]ormStruct, goTypes map[string]map[string]string) string {
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
		// R20：表上方关联结构体（可折叠核对字段映射）
		if sec := renderORMStructSectionMD(t, ormStructs[t]); sec != "" {
			b.WriteString(sec)
		}
		rows := mergeTableColumnsWithSchema(t, cols, cfg.Columns, schemas, goTypes)
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
func wikiGapReport(data []*domain.WikiModule, cfg wikiConfig, cols []*domain.TableColumn, schemas map[string]map[string]schemaCol) (modsNoDesc, tablesNoAlias, colsNoComment int) {
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
		for _, r := range mergeTableColumnsWithSchema(t.name, cols, tc.Columns, schemas, nil) {
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

// schemaCol 一个列的 schema 事实（R19：sqlite_master 解析）。
type schemaCol struct {
	Typ string
	Def string
}

// parseCreateTableSchema 解析 CREATE TABLE DDL → 每表列类型/默认值
// （R19：不借助 AI 填类型——SQLite schema 是权威事实源）。
// 跳过 FOREIGN KEY/PRIMARY KEY/UNIQUE/CHECK 等约束行；列定义
// `name TYPE [NOT NULL] [DEFAULT x]`——取第一个 token 为类型，
// DEFAULT 后（去注释）为默认值。
func parseCreateTableSchema(ddls map[string]string) map[string]map[string]schemaCol {
	out := map[string]map[string]schemaCol{}
	for table, ddl := range ddls {
		cols := map[string]schemaCol{}
		for _, line := range strings.Split(ddl, "\n") {
			line = strings.TrimSpace(line)
			line = strings.TrimSuffix(line, ",")
			// ALTER 加列形态：`, degrade_stats TEXT)`——前导逗号
			if strings.HasPrefix(line, ",") {
				line = strings.TrimSpace(strings.TrimPrefix(line, ","))
			}
			if line == "" || line == "(" || line == ")" {
				continue
			}
			up := strings.ToUpper(line)
			if strings.HasPrefix(up, "CREATE TABLE") || strings.HasPrefix(up, "FOREIGN KEY") ||
				strings.HasPrefix(up, "PRIMARY KEY") || strings.HasPrefix(up, "UNIQUE") ||
				strings.HasPrefix(up, "CHECK") || strings.HasPrefix(up, "CONSTRAINT") {
				continue
			}
			// 去行尾注释
			if i := strings.Index(line, "--"); i >= 0 {
				line = strings.TrimSpace(line[:i])
			}
			rawName := line
			if i := strings.IndexAny(line, " \t"); i >= 0 {
				rawName = line[:i]
			}
			// 先按原始名（含引号）算 rest 偏移，再 Trim 引号——
			// 否则引号列 rest 错位（Typ 取到 `"`）；整行无空格
			// （单 token 行）越界保护
			name := strings.Trim(rawName, `"`+"`[]")
			rest := ""
			if len(line) > len(rawName)+1 {
				rest = strings.TrimSpace(line[len(rawName)+1:])
			}
			if name == "" {
				continue
			}
			if rest == "" {
				continue
			}
			c := schemaCol{}
			// 类型 = 第一个 token（TEXT/INTEGER/REAL/JSON/BLOB…；
			// 注释在逗号后时逗号残留——去尾部逗号）
			if i := strings.IndexAny(rest, " \t"); i >= 0 {
				c.Typ = rest[:i]
			} else {
				c.Typ = rest
			}
			c.Typ = strings.TrimSuffix(c.Typ, ",")
			// DEFAULT 值
			if i := strings.Index(strings.ToUpper(rest), "DEFAULT"); i >= 0 {
				c.Def = strings.TrimSpace(rest[i+len("DEFAULT"):])
			}
			cols[name] = c
		}
		out[table] = cols
	}
	return out
}

// mergeTableColumnsWithSchema 列合并（R19）：类型/默认值填充优先级
// yaml > sqlite schema > gorm tag——schema 事实自动补全，yaml 人工可覆盖。
func mergeTableColumnsWithSchema(table string, cols []*domain.TableColumn, yamlCols []wikiTableColumn, schemas map[string]map[string]schemaCol, goTypes map[string]map[string]string) []tableColRow {
	rows := mergeTableColumns(table, cols, yamlCols)
	// ColType 索引（gorm tag——schema 缺失时的兜底）
	colType := map[string]string{}
	prefix := table + "."
	for _, c := range cols {
		if strings.HasPrefix(c.Name, prefix) {
			colType[strings.TrimPrefix(c.Name, prefix)] = c.ColType
		}
	}
	sc := schemas[table]
	goCols := goTypes[table] // R21：结构体 Go 类型（最终 fallback）
	for i := range rows {
		name := rows[i].name
		if rows[i].typ == "" {
			if sc != nil {
				if c, ok := sc[name]; ok {
					rows[i].typ = c.Typ // schema 优先
				} else if colType[name] != "" {
					rows[i].typ = colType[name] // schema 缺 → gorm tag
				} else {
					rows[i].typ = goCols[name] // 最终 → 结构体 Go 类型
				}
			} else if colType[name] != "" {
				rows[i].typ = colType[name]
			} else {
				rows[i].typ = goCols[name]
			}
		}
		if rows[i].def == "" && sc != nil {
			if c, ok := sc[name]; ok {
				rows[i].def = c.Def
			}
		}
	}
	return rows
}

// mergeTableColumns 表字段合并（#243 自动初稿 + yaml 覆盖）：
// 自动列（ER 表列虚拟节点：列名 + gorm tag 类型）为底，yaml columns
// 覆盖同名（type/default/comment 各自覆盖），自动列未列出的补全。
func mergeTableColumns(table string, cols []*domain.TableColumn, yamlCols []wikiTableColumn) []tableColRow {

	byName := map[string]tableColRow{}
	var order []string
	hidden := map[string]bool{}
	for _, c := range yamlCols {
		if c.Hidden {
			hidden[c.Name] = true // R3：yaml 显式隐藏的列（解析噪音等）自动列也过滤
			continue
		}
		byName[c.Name] = tableColRow{name: c.Name, typ: c.Type, def: c.Default, comment: c.Comment}
		order = append(order, c.Name)
	}

	prefix := table + "."
	for _, c := range cols {
		if !strings.HasPrefix(c.Name, prefix) {
			continue
		}
		col := strings.TrimPrefix(c.Name, prefix)
		if hidden[col] {
			continue
		}
		// R19：类型由 WithSchema 统一处理（schema 优先、ColType 兜底）——
		// 此处不填，避免覆盖优先级混乱
		if r, ok := byName[col]; ok {
			byName[col] = r
			continue
		}
		byName[col] = tableColRow{name: col}
		order = append(order, col)
	}
	out := make([]tableColRow, 0, len(order))
	for _, n := range order {
		out = append(out, byName[n])
	}
	return out
}

// wikiSchemas 表 schema 事实（R19）：sqlite_master CREATE TABLE 解析
// 为 列类型/默认值 映射（不借助 AI——SQLite 是权威）。
func wikiSchemas(acts *action.Actions) map[string]map[string]schemaCol {
	ddls, err := acts.TableSchemas()
	if err != nil {
		return nil
	}
	return parseCreateTableSchema(ddls)
}
