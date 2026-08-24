package cli

// #238 `codeintel wiki`——从代码生成业务 wiki（Markdown）。
// 数据：action.WikiData（六区块聚合）；wiki.yaml 是 AI 产出 → 人工确认
// 的契约（业务描述/模块别名/表别名/隐藏符号），无配置时纯自动生成。
// 输出：docs/wiki/index.md + 每模块一页 + tables.md。

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
	"github.com/schaepher/codeintel/internal/logging"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// cmdWiki 实现 `codeintel wiki [--repo <path>] [--out <dir>] [--yaml <file>]`。
func cmdWiki(args []string) int {
	logger := zap.L()
	logger.Debug("enter cmdWiki")
	defer logger.Debug("exit cmdWiki")
	repoPath := "."
	outDir := "docs/wiki"
	yamlPath := ""
	format := "md"
	initOnly := false
	aiMode := false
	aiAgent := ""
	aiWithQA := false
	diagram := "plantuml" // R32：图引擎（默认 plantuml，Q3 定案）
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
		case a == "--init":
			initOnly = true
		case a == "--ai":
			aiMode = true
		case a == "--agent" && i+1 < len(args):
			aiAgent = args[i+1]
			i++
		case strings.HasPrefix(a, "--agent="):
			aiAgent = strings.TrimPrefix(a, "--agent=")
		case a == "--with-qa":
			aiWithQA = true
		case a == "--diagram" && i+1 < len(args):
			diagram = args[i+1]
			i++
		case strings.HasPrefix(a, "--diagram="):
			diagram = strings.TrimPrefix(a, "--diagram=")
		case a == "--help" || a == "-h":
			fmt.Println("用法: codeintel wiki [--repo <path>] [--out <dir=docs/wiki>] [--yaml <file>] [--format md|html] [--init] [--ai] [--agent codex|claude|auto] [--with-qa] [--diagram plantuml|mermaid]\n  从代码生成业务 wiki（Markdown 或单文件 HTML）——wiki.yaml 可补充业务描述/别名/隐藏符号；--init 生成 wiki.yaml 骨架；--ai 用 AI 增量补缺（无描述模块/无别名表/无说明列/术语表 → 写回 wiki.yaml 标注 # AI 初稿）；--with-qa 从历史问答（ask/serve 对话收集）读取相关 Q&A 作为参考资料；--diagram 图引擎（默认 plantuml——HTML 渲染 PNG 嵌入；mermaid 浏览器端渲染）")
			return 0
		default:
			fmt.Fprintf(os.Stderr, "error: 未知参数 %q\n", a)
			return 2
		}
	}
	if diagram != "plantuml" && diagram != "mermaid" {
		fmt.Fprintf(os.Stderr, "error: 未知 diagram %q（支持 plantuml|mermaid）\n", diagram)
		return 2
	}
	abs, _, err := resolveRepo(repoPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		if repoPath == "." {
			printRepoHint()
		}
		return 1
	}
	if initOnly {
		return cmdWikiInit(abs)
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
	} else if b, err := os.ReadFile(filepath.Join(abs, "wiki.yaml")); err == nil {
		// B：与 serve 对齐——仓库根 wiki.yaml 自动发现（存在即用；
		// 否则纯自动生成，丢人工描述）
		if err := yaml.Unmarshal(b, &cfg); err != nil {
			fmt.Fprintf(os.Stderr, "error: 解析 wiki.yaml: %v\n", err)
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
	// #0 wiki --ai：缺口收集 → AI 初稿 → 合并 wiki.yaml（先补缺再渲染，
	// 渲染用更新后的 cfg）
	if aiMode {
		agent, err := resolveAgent(aiAgent)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		yp := yamlPath
		if yp == "" {
			yp = filepath.Join(abs, "wiki.yaml")
		}
		okN, skipN, failN := wikiAIFill(yp, &cfg, data, cols, rels, agent, aiTimeout, aiWithQA, sqlite.NewRepo(db))
		fmt.Printf("wiki --ai：补全 %d 条、跳过 %d 条、失败 %d 条（已写回 %s，标注 # AI 初稿——git diff 可回滚）\n",
			okN, skipN, failN, yp)
	}
	// R1：包职责地图（包节点 doc_comment）
	pkgs, err := acts.Packages()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	// 新鲜度标注：基于索引 commit（wiki 产物是索引快照，注明版本）
	freshNote := ""
	degradeStats := ""
	if latest, err := acts.Latest(); err == nil && latest.CommitSHA != "" {
		freshNote = "索引 commit: " + shortSHA(latest.CommitSHA)
		degradeStats = latest.DegradeStats // R6：构建降级可观测
	}
	rc := &wikiRenderCtx{acts: acts, data: data, cfg: cfg, cols: cols, rels: rels, pkgs: pkgs, freshNote: freshNote, degradeStats: degradeStats, Diagram: diagram}
	switch format {
	case "html":
		if err := renderWikiHTML(abs, outDir, rc); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
	case "md", "":
		if err := renderWiki(abs, outDir, rc); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
	default:
		fmt.Fprintf(os.Stderr, "error: 未知 format %q（支持 md|html）\n", format)
		return 2
	}
	fmt.Printf("wiki 已生成: %s（%d 个模块）\n", outDir, len(data))
	// D：描述补全引导——缺口统计提示（无描述模块/无别名表/无说明表列）
	if mods, tbls, cc := wikiGapReport(data, cfg, cols, nil); mods+tbls+cc > 0 {
		fmt.Printf("wiki 补全提示：%d 个模块无描述、%d 张表无别名、%d 个表列无说明——在 wiki.yaml 补充后重新生成\n", mods, tbls, cc)
	}
	return 0
}

// wikiRenderCtx 渲染上下文（R1 起统一：模块/配置/表列/关系/包地图/新鲜度）。
type wikiRenderCtx struct {
	acts         *action.Actions // R2：流程页调用链查询
	degradeStats string          // R6：构建降级统计 JSON（SQL 解析）
	data      []*domain.WikiModule
	cfg       wikiConfig
	cols      []*domain.TableColumn
	rels      []*domain.TableRelation
	pkgs      []*domain.CodeEntity // R1：包职责地图（GetPackages）
	freshNote string
	Diagram  string // R32：图引擎 plantuml（默认）| mermaid
}

// diagramMD md 图块：plantuml 模式输出 ```plantuml 文本（md 不嵌 PNG）；
// mermaid 模式原样代码块。
func (rc *wikiRenderCtx) diagramMD(mermaid string) string {
	if rc.Diagram != "plantuml" {
		return "```mermaid\n" + mermaid + "\n```\n\n"
	}
	return "```plantuml\n" + mermaidToPlantuml(mermaid) + "\n```\n\n"
}

// diagramHTML html 图块：plantuml 模式渲染 PNG → base64 <img>（单文件
// 自包含；渲染失败降级 plantuml 文本块）；mermaid 模式 <pre class="mermaid">。
func (rc *wikiRenderCtx) diagramHTML(mermaid string) string {
	if rc.Diagram != "plantuml" {
		return "<pre class=\"mermaid\">" + htmlEsc(mermaid) + "</pre>"
	}
	puml := mermaidToPlantuml(mermaid)
	if puml == "" {
		return "<pre class=\"mermaid\">" + htmlEsc(mermaid) + "</pre>"
	}
	if png, err := plantumlRender(puml); err == nil {
		return "<img src=\"data:image/png;base64," + base64.StdEncoding.EncodeToString(png) + "\" alt=\"diagram\"/>"
	}
	return "<pre class=\"plantuml\">" + htmlEsc(puml) + "</pre>"
}

// renderWiki 生成 index.md + 模块页 + tables.md + er.md + commands.md +
// api.md（全量覆盖）。

// tableCfgsFrom 从 yaml 构建表配置索引（name → 配置）。
func tableCfgsFrom(cfg wikiConfig) map[string]wikiTableConfig {
	out := map[string]wikiTableConfig{}
	for _, t := range cfg.Tables {
		out[t.Name] = t
	}
	return out
}
