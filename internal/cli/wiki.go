package cli

// #238 `codeintel wiki`——从代码生成业务 wiki（Markdown）。
// 数据：action.WikiData（六区块聚合）；wiki.yaml 是 AI 产出 → 人工确认
// 的契约（业务描述/模块别名/表别名/隐藏符号），无配置时纯自动生成。
// 输出：docs/wiki/index.md + 每模块一页 + tables.md。

import (
	"errors"
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
	// Q251：ER 图页面关系数据（复用已算；未算同步兜底计算）
	rels, err := wikiRelations(acts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	switch format {
	case "html":
		if err := renderWikiHTML(abs, outDir, data, cfg, cols, rels); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
	case "md", "":
		if err := renderWiki(abs, outDir, data, cfg, cols, rels); err != nil {
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

// wikiRelations 获取全库表间关联（Q251 ER 图页面）：优先复用已算
// relation_candidates；未算（ErrRelationInProgress）时同步兜底计算
// （wiki 是批处理命令，直接等结果；serve 的异步兜底不适合）。
func wikiRelations(acts *action.Actions) ([]*domain.TableRelation, error) {
	rels, err := acts.RelationsAll("")
	if err == nil {
		return rels, nil
	}
	if !errors.Is(err, domain.ErrRelationInProgress) {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "正在计算表间关系（ER 图首次生成需要）…\n")
	if err := acts.PrecomputeAllRelations(nil); err != nil {
		return nil, err
	}
	rels, err = acts.RelationsAll("")
	if err != nil {
		// 无 build_metadata 的仓库（fixture/未 init）finish 跳过置
		// done——ER 图降级为空（不阻塞 wiki 生成），stderr 提示
		fmt.Fprintf(os.Stderr, "warning: ER 图关系数据不可用: %v\n", err)
		return nil, nil
	}
	return rels, nil
}

// renderERMermaid ER 图 mermaid（Q251）：表实体 + 关系线（仅直接键
// 关联 fk/query，列级标注 label；write/read 间接关联不画）。隐藏表
// 过滤；确定性排序。
func renderERMermaid(rels []*domain.TableRelation, hideTable map[string]bool) string {
	tables := map[string]bool{}
	var lines []string
	for _, r := range rels {
		if r.Type != domain.RelationFK && r.Type != domain.RelationQuery {
			continue
		}
		if hideTable[r.FromTable] || hideTable[r.ToTable] {
			continue
		}
		tables[r.FromTable] = true
		tables[r.ToTable] = true
		lines = append(lines, fmt.Sprintf("    %s ||--o{ %s : \"%s → %s [%s]\"",
			r.FromTable, r.ToTable, r.FromCol, r.ToCol, r.Type))
	}
	if len(tables) == 0 {
		return "erDiagram\n"
	}
	var sb strings.Builder
	sb.WriteString("erDiagram\n")
	var names []string
	for t := range tables {
		names = append(names, t)
	}
	sort.Strings(names)
	for _, t := range names {
		sb.WriteString("    " + t + "\n")
	}
	sort.Strings(lines)
	for _, l := range lines {
		sb.WriteString(l + "\n")
	}
	return sb.String()
}

// renderERPage ER 图页面（Q251，er.md）：erDiagram + 关系明细表
// （仅 fk/query，隐藏表过滤）。字段详情见 tables.md。
func renderERPage(rels []*domain.TableRelation, hideTable map[string]bool) string {
	var b strings.Builder
	b.WriteString("# ER 图（表间关系）\n\n")
	b.WriteString("表间直接键关联（fk=值流验证的真实键 / query=WHERE 键关联），列级标注。字段定义与建表语句见[表清单](tables.md)。\n\n")
	m := renderERMermaid(rels, hideTable)
	if !strings.Contains(m, "||--") {
		b.WriteString("（无表间直接关联）\n\n")
	} else {
		b.WriteString("```mermaid\n" + m + "```\n\n")
	}
	b.WriteString("## 关系明细\n\n")
	b.WriteString("| 本表 | 本表列 | 关联表 | 关联列 | 类型 |\n|---|---|---|---|---|\n")
	type row struct{ a, b, c, d, e string }
	var rows []row
	for _, r := range rels {
		if r.Type != domain.RelationFK && r.Type != domain.RelationQuery {
			continue
		}
		if hideTable[r.FromTable] || hideTable[r.ToTable] {
			continue
		}
		rows = append(rows, row{r.FromTable, r.FromCol, r.ToTable, r.ToCol, r.Type})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].a != rows[j].a {
			return rows[i].a < rows[j].a
		}
		if rows[i].c != rows[j].c {
			return rows[i].c < rows[j].c
		}
		return rows[i].b < rows[j].b
	})
	for _, r := range rows {
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s |\n", r.a, r.b, r.c, r.d, r.e))
	}
	return b.String()
}

// renderWiki 生成 index.md + 模块页 + tables.md + er.md（全量覆盖）。
func renderWiki(repoAbs, outDir string, data []*domain.WikiModule, cfg wikiConfig, cols []*domain.TableColumn, rels []*domain.TableRelation) error {
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
	idx.WriteString("- [ER 图（表间关系）](er.md)\n")
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
	if err := os.WriteFile(filepath.Join(outDir, "tables.md"), []byte(tables), 0o644); err != nil {
		return err
	}
	// ER 图页面（Q251）
	er := renderERPage(rels, hideTable)
	return os.WriteFile(filepath.Join(outDir, "er.md"), []byte(er), 0o644)
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
	b.WriteString("## 架构图（包间调用）\n\n")
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

// moduleArchMermaid 模块页架构图（Q251-A：包间调用图——calls 边按
// 包聚合，线上标调用次数；替代空模块级 gRPC 图）。
func moduleArchMermaid(wm *domain.WikiModule) string {
	if len(wm.PkgCalls) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("graph LR\n")
	for _, c := range wm.PkgCalls {
		b.WriteString(fmt.Sprintf("  %s -->|%d| %s\n", archNode(c.From), c.Count, archNode(c.To)))
	}
	return b.String()
}

// archNode mermaid 节点（Q251 补：`[cli]` 纯方括号是非法语法——
// mermaid 要求 id[文本] 形态；id 用短名保证唯一）。
func archNode(name string) string {
	return name + "[" + name + "]"
}

// shortMod module 路径末段（渲染用）。
func shortMod(mod string) string {
	if i := strings.LastIndex(mod, "/"); i >= 0 {
		return mod[i+1:]
	}
	return mod
}

// sequenceMermaid 调用链 → sequenceDiagram（参与者 + 边，确定性排序）。
// sequenceMermaid 自动时序（Q251 补）：参与者含括号符号名
// （(Actions).BatchSymbols）直接出现在消息行是语法错误——参与者
// 别名化（P0/P1… + participant P0 as "显示名"），消息行用别名。
func sequenceMermaid(steps []domain.WikiSeqStep) string {
	var b strings.Builder
	b.WriteString("sequenceDiagram\n")
	alias := map[string]string{}
	var order []string
	for _, st := range steps {
		for _, p := range []string{st.Caller, st.Callee} {
			if _, ok := alias[p]; !ok {
				alias[p] = fmt.Sprintf("P%d", len(order))
				order = append(order, p)
			}
		}
	}
	for _, p := range order {
		b.WriteString(fmt.Sprintf("  participant %s as \"%s\"\n", alias[p], p))
	}
	for _, st := range steps {
		b.WriteString(fmt.Sprintf("  %s->>%s: call\n", alias[st.Caller], alias[st.Callee]))
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
