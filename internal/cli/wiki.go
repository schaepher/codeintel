package cli

// #238 `codeintel wiki`——从代码生成业务 wiki（Markdown）。
// 数据：action.WikiData（六区块聚合）；wiki.yaml 是 AI 产出 → 人工确认
// 的契约（业务描述/模块别名/表别名/隐藏符号），无配置时纯自动生成。
// 输出：docs/wiki/index.md + 每模块一页 + tables.md。

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
	"github.com/schaepher/codeintel/internal/logging"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// wikiConfig wiki.yaml 契约（AI 产出 → 人工最后确认微调）。
type wikiConfig struct {
	Project struct {
		Description string `yaml:"description"`
	} `yaml:"project"`
	Modules []struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
		Order       int    `yaml:"order"`
	} `yaml:"modules"`
	Tables        []wikiTableConfig `yaml:"tables"`
	HiddenSymbols []string          `yaml:"hidden_symbols"`
	// 架构图（mermaid 代码块；为空时自动从模块间调用生成）
	Architecture string `yaml:"architecture"`
	// 业务流程时序（业务语义，代码画不出——维护者模式访谈产出）
	Flows []struct {
		Title  string `yaml:"title"`
		Mermaid string `yaml:"mermaid"`
	} `yaml:"flows"`
	// 术语表（#246 业务黑话解释：ssa/ast/ER 等）
	Glossary []struct {
		Term       string `yaml:"term"`
		Definition string `yaml:"definition"`
	} `yaml:"glossary"`
}

// wikiTableConfig 表结构契约（#243 表详情：字段定义/索引/建表语句——
// 业务表 schema 在外部库，代码分析不出，AI 调研产出 + 人工确认）。
type wikiTableConfig struct {
	Name    string `yaml:"name"`
	Alias   string `yaml:"alias"`
	Hidden  bool   `yaml:"hidden"` // #245 噪音表隐藏（fixture 等）
	Columns []struct {
		Name    string `yaml:"name"`
		Type    string `yaml:"type"`
		Default string `yaml:"default"`
		Comment string `yaml:"comment"`
	} `yaml:"columns"`
	Indexes []string `yaml:"indexes"`
	DDL     string   `yaml:"ddl"`
}

// wikiMeta 渲染用的模块增强信息（yaml 合并结果）。
type wikiMeta struct {
	desc  string
	order int
}

// cmdWiki 实现 `codeintel wiki [--repo <path>] [--out <dir>] [--yaml <file>]`。
func cmdWiki(args []string) int {
	logger := zap.L()
	logger.Debug("enter cmdWiki")
	defer logger.Debug("exit cmdWiki")
	repoPath := "."
	outDir := "docs/wiki"
	yamlPath := ""
	format := "md"
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--repo" && i+1 < len(args):
			repoPath = ResolveRepoRef(args[i+1])
			i++
		case strings.HasPrefix(a, "--repo="):
			repoPath = ResolveRepoRef(strings.TrimPrefix(a, "--repo="))
		case a == "--out" && i+1 < len(args):
			outDir = args[i+1]
			i++
		case strings.HasPrefix(a, "--out="):
			outDir = strings.TrimPrefix(a, "--out=")
		case a == "--yaml" && i+1 < len(args):
			yamlPath = args[i+1]
			i++
		case strings.HasPrefix(a, "--yaml="):
			yamlPath = strings.TrimPrefix(a, "--yaml=")
		case a == "--format" && i+1 < len(args):
			format = args[i+1]
			i++
		case strings.HasPrefix(a, "--format="):
			format = strings.TrimPrefix(a, "--format=")
		case a == "--help" || a == "-h":
			fmt.Println("用法: codeintel wiki [--repo <path>] [--out <dir=docs/wiki>] [--yaml <file>] [--format md|html]\n  从代码生成业务 wiki（Markdown 或单文件 HTML）——wiki.yaml 可补充业务描述/别名/隐藏符号")
			return 0
		default:
			fmt.Fprintf(os.Stderr, "error: 未知参数 %q\n", a)
			return 2
		}
	}
	abs, _, err := resolveRepo(repoPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		if repoPath == "." {
			printRepoHint()
		}
		return 1
	}
	if err := logging.ToFile(abs); err != nil {
		fmt.Fprintf(os.Stderr, "warning: 日志切换失败: %v\n", err)
	}
	db, err := sqlite.Open(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()
	acts := action.New(sqlite.NewRepo(db))
	repo, err := buildRepo(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	mods := repo.Modules
	data, err := acts.WikiData(mods)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	cfg := wikiConfig{}
	if yamlPath != "" {
		b, err := os.ReadFile(yamlPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: 读取 %s: %v\n", yamlPath, err)
			return 1
		}
		if err := yaml.Unmarshal(b, &cfg); err != nil {
			fmt.Fprintf(os.Stderr, "error: 解析 %s: %v\n", yamlPath, err)
			return 1
		}
	}
	// yaml 模块白名单：列出则只生成这些模块（fixture/子模块噪音过滤）
	if len(cfg.Modules) > 0 {
		want := map[string]bool{}
		for _, m := range cfg.Modules {
			want[m.Name] = true
		}
		var filtered []*domain.WikiModule
		for _, wm := range data {
			if want[wm.Name] {
				filtered = append(filtered, wm)
			}
		}
		data = filtered
	}
	cols, err := acts.GetAllTableColumns()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	switch format {
	case "html":
		if err := renderWikiHTML(abs, outDir, data, cfg, cols); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
	case "md", "":
		if err := renderWiki(abs, outDir, data, cfg, cols); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
	default:
		fmt.Fprintf(os.Stderr, "error: 未知 format %q（支持 md|html）\n", format)
		return 2
	}
	fmt.Printf("wiki 已生成: %s（%d 个模块）\n", outDir, len(data))
	return 0
}

// renderWiki 生成 index.md + 模块页 + tables.md（全量覆盖）。
func renderWiki(repoAbs, outDir string, data []*domain.WikiModule, cfg wikiConfig, cols []*domain.TableColumn) error {
	logger := zap.L()
	logger.Debug("enter renderWiki", zap.Int("modules", len(data)))
	defer logger.Debug("exit renderWiki")
	// 全量覆盖语义：清空输出目录（防旧模块页残留）
	if err := os.RemoveAll(outDir); err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	// yaml 索引：模块描述/顺序、表别名、隐藏符号（md/html 共用）
	meta, tableAlias, hidden := wikiMetaIndex(cfg)
	tableCfgs := tableCfgsFrom(cfg)
	// #245 表噪音过滤：hidden 表从模块相关表与表清单移除
	hideTable := map[string]bool{}
	for _, t := range cfg.Tables {
		if t.Hidden {
			hideTable[t.Name] = true
		}
	}
	if len(hideTable) > 0 {
		for _, wm := range data {
			var kept []string
			for _, t := range wm.Tables {
				if !hideTable[t] {
					kept = append(kept, t)
				}
			}
			wm.Tables = kept
		}
	}
	// 模块页（按 order 排序）
	ordered := append([]*domain.WikiModule(nil), data...)
	sort.SliceStable(ordered, func(i, j int) bool {
		oi, oj := meta[ordered[i].Name].order, meta[ordered[j].Name].order
		if oi != oj {
			return oi != 0 && (oj == 0 || oi < oj)
		}
		return ordered[i].Name < ordered[j].Name
	})
	var idx strings.Builder
	idx.WriteString("# " + filepath.Base(repoAbs) + " 业务 wiki\n\n")
	if cfg.Project.Description != "" {
		idx.WriteString(cfg.Project.Description + "\n\n")
	}
	idx.WriteString("**快速开始**：① 看[架构图](#整体架构图)了解系统组成 → ② 按顺序读各模块（职责 → 入口 → 核心符号 → 相关表）→ ③ 查[表清单](tables.md)看字段与建表语句。\n\n")
	if cfg.Architecture != "" {
		idx.WriteString("## 整体架构图\n\n```mermaid\n" + cfg.Architecture + "\n```\n\n")
	}
	idx.WriteString("由 `codeintel wiki` 生成（全量覆盖；业务描述/别名维护在 wiki.yaml）\n\n")
	idx.WriteString("## 模块\n\n")
	for _, wm := range ordered {
		idx.WriteString(fmt.Sprintf("- [%s](%s.md)", wm.Name, wm.ShortName))
		if d := meta[wm.Name].desc; d != "" {
			idx.WriteString(" — " + d)
		}
		idx.WriteString("\n")
	}
	idx.WriteString("\n## 表\n\n")
	idx.WriteString("- [表清单](tables.md)\n")
	if len(cfg.Glossary) > 0 {
		idx.WriteString("\n## 术语表\n\n")
		for _, g := range cfg.Glossary {
			idx.WriteString(fmt.Sprintf("- **%s**：%s\n", g.Term, g.Definition))
		}
	}
	for _, wm := range data {
		page := renderModulePage(wm, meta[wm.Name].desc, tableAlias, hidden, cfg)
		if err := os.WriteFile(filepath.Join(outDir, wm.ShortName+".md"), []byte(page), 0o644); err != nil {
			return err
		}
	}
	// 表附录
	idx.WriteString("\n---\n\n由 codeintel wiki 生成 · 重新生成前请确认 wiki.yaml\n")
	if err := os.WriteFile(filepath.Join(outDir, "index.md"), []byte(idx.String()), 0o644); err != nil {
		return err
	}
	tables := renderTablesPage(data, tableAlias, tableCfgs, cols)
	return os.WriteFile(filepath.Join(outDir, "tables.md"), []byte(tables), 0o644)
}

// tableCfgsFrom 从 yaml 构建表配置索引（name → 配置）。
func tableCfgsFrom(cfg wikiConfig) map[string]wikiTableConfig {
	out := map[string]wikiTableConfig{}
	for _, t := range cfg.Tables {
		out[t.Name] = t
	}
	return out
}

// renderModulePage 模块页六区块 + 架构图 + 流程时序。
func renderModulePage(wm *domain.WikiModule, desc string, tableAlias map[string]string, hidden map[string]bool, cfg wikiConfig) string {
	var b strings.Builder
	b.WriteString("# " + wm.Name + "\n\n")
	if desc != "" {
		b.WriteString("> " + desc + "\n\n")
	}
	b.WriteString("## 职责\n\n")
	if desc != "" {
		b.WriteString(desc + "\n\n")
	}
	if wm.Desc != "" {
		b.WriteString(wm.Desc + "\n\n")
	}
	if desc == "" && wm.Desc == "" {
		b.WriteString("（无描述——维护者可在 wiki.yaml modules.description 补充）\n\n")
	}
	if len(wm.Entries) > 0 {
		b.WriteString("## 入口\n\n")
		for _, e := range wm.Entries {
			b.WriteString("- `" + e + "`\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("## 核心符号（内部实现参考——被调用最多）\n\n")
	if len(wm.CoreSymbols) > 0 {
		b.WriteString("| 符号 | 类型 | 调用者数 | 位置 |\n|---|---|---|---|\n")
		for _, s := range wm.CoreSymbols {
			if hidden[s.Name] {
				continue
			}
			loc := ""
			if s.File != "" {
				loc = fmt.Sprintf("%s:%d", s.File, s.Line)
			}
			b.WriteString(fmt.Sprintf("| %s | %s | %d | %s |\n", s.Name, s.Kind, s.Callers, loc))
		}
		b.WriteString("\n")
	} else {
		b.WriteString("（无调用数据）\n\n")
	}
	if len(wm.OutCalls) > 0 {
		b.WriteString("## 调用的模块\n\n")
		for _, m := range wm.OutCalls {
			b.WriteString("- `" + m + "`\n")
		}
		b.WriteString("\n")
	}
	if len(wm.InCalls) > 0 {
		b.WriteString("## 被哪些模块调用\n\n")
		for _, m := range wm.InCalls {
			b.WriteString("- `" + m + "`\n")
		}
		b.WriteString("\n")
	}
	if len(wm.Tables) > 0 {
		b.WriteString("## 相关表\n\n")
		for _, t := range wm.Tables {
			line := "- [`" + t + "`](tables.md#" + t + ")"
			if a := tableAlias[t]; a != "" {
				line += "（" + a + "）"
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}
	// 架构图（#248：yaml 全局图在 index；模块页只渲染自动模块间调用图）
	b.WriteString("## 架构图（模块间调用）\n\n")
	arch := moduleArchMermaid(wm)
	if arch != "" {
		b.WriteString("```mermaid\n" + arch + "\n```\n\n")
	} else {
		b.WriteString("（单模块或无线索；整体架构见 index 架构图）\n\n")
	}
	// 流程时序（#242）：yaml 业务时序各自单独 + 自动时序每个一级调用
	// 分支单独一张图
	b.WriteString("## 流程时序\n\n")
	hasSeq := false
	for _, f := range cfg.Flows {
		b.WriteString("### " + f.Title + "\n\n")
		b.WriteString("```mermaid\n" + f.Mermaid + "\n```\n\n")
		hasSeq = true
	}
	if len(wm.Flows) > 0 {
		b.WriteString("（自动生成：内部调用链——代码事实，帮助理解模块怎么运转；业务时序见上方 yaml flows）\n\n")
	}
	for _, fl := range wm.Flows {
		b.WriteString("### 内部调用链：" + fl.Title + "\n\n")
		b.WriteString("```mermaid\n" + sequenceMermaid(fl.Steps) + "\n```\n\n")
		hasSeq = true
	}
	if !hasSeq {
		b.WriteString("（无调用链——yaml flows 可手写业务时序）\n\n")
	}
	return b.String()
}

// moduleArchMermaid 自动模块架构图（模块间调用，graph LR）。
func moduleArchMermaid(wm *domain.WikiModule) string {
	var b strings.Builder
	b.WriteString("graph LR\n")
	seen := map[string]bool{}
	for _, m := range wm.OutCalls {
		if !seen[m] {
			seen[m] = true
			b.WriteString("  " + archNode(wm.ShortName) + " --> " + archNode(shortMod(m)) + "\n")
		}
	}
	for _, m := range wm.InCalls {
		if !seen["in:"+m] {
			seen["in:"+m] = true
			b.WriteString("  " + archNode(shortMod(m)) + " --> " + archNode(wm.ShortName) + "\n")
		}
	}
	return b.String()
}

// archNode mermaid 节点（短名做 id，中文/特殊字符安全：用短名 id + 标签）。
func archNode(name string) string {
	return "[" + name + "]"
}

// shortMod module 路径末段（渲染用）。
func shortMod(mod string) string {
	if i := strings.LastIndex(mod, "/"); i >= 0 {
		return mod[i+1:]
	}
	return mod
}

// sequenceMermaid 调用链 → sequenceDiagram（参与者 + 边，确定性排序）。
func sequenceMermaid(steps []domain.WikiSeqStep) string {
	var b strings.Builder
	b.WriteString("sequenceDiagram\n")
	for _, st := range steps {
		b.WriteString("  " + st.Caller + "->>" + st.Callee + ": call\n")
	}
	return b.String()
}

// renderTablesPage 表清单 + 每表详情（字段定义表/索引/建表语句，#243）。
func renderTablesPage(data []*domain.WikiModule, tableAlias map[string]string, tableCfgs map[string]wikiTableConfig, cols []*domain.TableColumn) string {
	var b strings.Builder
	b.WriteString("# 表清单\n\n")
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
	// #249：yaml 手写定义的表也渲染（自动未发现时用户已维护 schema）
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
	// 每表详情小节
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
	// yaml 索引（同名覆盖）
	byName := map[string]tableColRow{}
	var order []string
	for _, c := range yamlCols {
		byName[c.Name] = tableColRow{name: c.Name, typ: c.Type, def: c.Default, comment: c.Comment}
		order = append(order, c.Name)
	}
	// 自动列补全（同名合并：yaml 有值则保留，空则用自动）
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
