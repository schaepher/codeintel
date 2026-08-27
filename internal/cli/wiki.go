package cli

// #238 `codeintel wiki`——从代码生成业务 wiki（Markdown）。
// 数据：action.WikiData（六区块聚合）；wiki.yaml 是 AI 产出 → 人工确认
// 的契约（业务描述/模块别名/表别名/隐藏符号），无配置时纯自动生成。
// 输出：docs/wiki/index.md + 每模块一页 + tables.md。

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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
	maxEntries := 0       // R37：流程页入口展开上限（0 = 默认 15）
	seqDepth := 0         // R83：grpc 方法代码级时序嵌套层级（0 = 全局配置，默认 3）
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
		case a == "--max-entries" && i+1 < len(args):
			maxEntries, _ = strconv.Atoi(args[i+1])
			i++
		case a == "--seq-depth" && i+1 < len(args):
			seqDepth, _ = strconv.Atoi(args[i+1])
			i++
		case strings.HasPrefix(a, "--max-entries="):
			maxEntries, _ = strconv.Atoi(strings.TrimPrefix(a, "--max-entries="))
		case a == "--help" || a == "-h":
			fmt.Println("用法: codeintel wiki [--repo <path>] [--out <dir=docs/wiki>] [--yaml <file>] [--format md|html] [--init] [--ai] [--agent codex|claude|auto] [--with-qa] [--diagram plantuml|mermaid] [--max-entries <N>]\n  从代码生成业务 wiki（Markdown 或单文件 HTML）——wiki.yaml 可补充业务描述/别名/隐藏符号；--init 生成 wiki.yaml 骨架；--ai 用 AI 增量补缺（无描述模块/无别名表/无说明列/术语表 → 写回 wiki.yaml 标注 # AI 初稿；ai.fill 可细分到类别 modules/tables/columns/glossary）；--with-qa 从历史问答（ask/serve 对话收集）读取相关 Q&A 作为参考资料；--diagram 图引擎（默认 plantuml——HTML 渲染 PNG 嵌入；mermaid 浏览器端渲染）；--max-entries 流程页每节/每页入口展开上限（默认 15，超出折叠为清单）；wiki.yaml 必须配置 domains（业务域）才能生成——先跑 `codeintel domains --prompt \"<用户约束>\"`（可预先指定部分域）生成 AI 初稿并确认；AI 使用点可用配置关闭（wiki.yaml 或 ~/.codeintel/config.yaml 的 ai: {domains, fill, ask: auto|off}，fill 可细分）")
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
	// R57：业务域前置检查——wiki.yaml（或 --yaml）必须配置 domains，
	// 否则不允许继续生成（不再自动调 AI 分析：domains 是 AI 初稿 →
	// 人工确认的契约，未配置说明未完成确认）。生成手段：先跑
	// `codeintel domains --prompt "<约束>"` 或手动在 wiki.yaml 配置
	if len(cfg.Domains) == 0 && !initOnly {
		fmt.Fprintf(os.Stderr, "error: wiki.yaml 未配置 domains（业务域）——不允许生成。请先运行 `codeintel domains --prompt \"<用户约束>\"` 生成 AI 初稿并确认，或手动在 wiki.yaml 配置 domains 区块\n")
		return 1
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
	// 渲染用更新后的 cfg）。R56：ai.fill=off（wiki.yaml/全局）→ 整步
	// 跳过（不调 resolveAgent）
	if aiMode {
		if !aiEnabled("fill", cfg) {
			fmt.Println("ai.fill=off——跳过 AI 补缺（wiki.yaml 或 ~/.codeintel/config.yaml 配置）")
		} else {
			agent, err := resolveAgent(aiAgent)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return 1
			}
			yp := yamlPath
			if yp == "" {
				yp = filepath.Join(abs, "wiki.yaml")
			}
			okN, skipN, failN := wikiAIFill(yp, &cfg, data, cols, rels, agent, aiTimeout, aiWithQA, acts, abs)
			fmt.Printf("wiki --ai：补全 %d 条、跳过 %d 条、失败 %d 条（已写回 %s，标注 # AI 初稿——git diff 可回滚）\n",
				okN, skipN, failN, yp)
		}
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
	if seqDepth <= 0 {
		seqDepth = loadSeqDepth()
	}
	rc := &wikiRenderCtx{acts: acts, data: data, cfg: cfg, cols: cols, rels: rels, pkgs: pkgs, freshNote: freshNote, degradeStats: degradeStats, Diagram: diagram, MaxEntries: maxEntries, RepoAbs: abs, SeqDepth: seqDepth, SeqStopPkgs: loadSeqStopPkgs(), SeqFilter: loadSeqFilter()}
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
// R100：数据源全部经 action（acts）——cli 不持 sqlite.Repo。
type wikiRenderCtx struct {
	acts         *action.Actions // R2：流程页调用链查询（全部数据源经此）
	degradeStats string          // R6：构建降级统计 JSON（SQL 解析）
	data         []*domain.WikiModule
	cfg          wikiConfig
	cols         []*domain.TableColumn
	rels         []*domain.TableRelation
	pkgs         []*domain.CodeEntity // R1：包职责地图（GetPackages）
	freshNote    string
	Diagram      string // R32：图引擎 plantuml（默认）| mermaid
	MaxEntries   int    // R37：流程页每节/每页入口展开上限（0 = procMaxEntries）
	SeqDepth     int          // R83：grpc 方法代码级时序嵌套层级（默认 3——loadSeqDepth）
	// R99：plantuml 转换失败即停——diagramMD/diagramHTML 记录首个错误，
	// 渲染入口检查后中止（不产出部分成功的 wiki）
	renderErr   error
	SeqStopPkgs []string       // R95：时序停止包（loadSeqStopPkgs——命中不深入）
	SeqFilter   action.SeqFilter // R100：时序过滤（loadSeqFilter——命中不生成节点；wiki 与 query sequence --code 同源）
	RepoAbs     string         // R37：目标仓库绝对路径（grpc ServiceDesc 解析需要——空则方法全集缺失）
}

// diagramMD/diagramHTML 已拆到 wiki_diagram.go（行数治理）。

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
