package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
	"go.uber.org/zap"
)

// renderTablesPage 表清单 + 每表详情（字段定义表/索引/建表语句，#243）。
func renderTablesPage(data []*domain.WikiModule, tableAlias map[string]string, tableCfgs map[string]wikiTableConfig, cols []*domain.TableColumn, schemas map[string]map[string]schemaCol, ormStructs map[string][]domain.ORMStruct, goTypes map[string]map[string]string) string {
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
		rows := mergeTableColumnsWithSchema(t, cols, cfg.Columns, schemas, ormStructs)
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

// wikiSchemas 表 schema 事实（R19）：sqlite_master CREATE TABLE 解析
// 为 列类型/默认值 映射（不借助 AI——SQLite 是权威）。
// P1-05：再合并全局注册表（~/.codeintel）的 schema——注册表 repos
// 等仓库外表在仓库内没有事实源。
func wikiSchemas(acts *action.Actions) map[string]map[string]schemaCol {
	ddls, err := acts.TableSchemas()
	if err != nil {
		// 仓库 schema 读取失败不阻断：仍可用全局注册表事实源
		zap.L().Warn("读取仓库表 schema 失败（wiki 表类型继续用其他事实源）", zap.Error(err))
		ddls = nil
	}
	return parseCreateTableSchema(mergeSchemaDDLs(ddls, globalSchemaDDLs()))
}

// globalSchemaDDLs 全局注册表（~/.codeintel/codeintel.db）的建表语句。
// 目录不可用/库不存在/读取失败都返回 nil（wiki 只缺这部分类型，不失败）。
func globalSchemaDDLs() map[string]string {
	dir := registryDirFn()
	if dir == "" {
		return nil
	}
	ddls, err := sqlite.RegistryTableSchemas(dir)
	if err != nil {
		zap.L().Warn("读取全局注册表 schema 失败（忽略仓库外表类型）", zap.Error(err))
		return nil
	}
	return ddls
}

// mergeSchemaDDLs 合并 schema 事实源：base（仓库内）优先——同名表不被
// extra（仓库外）覆盖（被分析仓库自己的 DDL 才是该表的权威）。
// 返回新 map（不改入参，并发调用安全）。
func mergeSchemaDDLs(base, extra map[string]string) map[string]string {
	if len(extra) == 0 {
		return base
	}
	out := make(map[string]string, len(base)+len(extra))
	for name, ddl := range base {
		out[name] = ddl
	}
	for name, ddl := range extra {
		if _, ok := out[name]; ok {
			continue
		}
		out[name] = ddl
	}
	return out
}
