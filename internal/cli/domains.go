package cli

// R34 `codeintel domains`——AI 业务域分析（批次 C：编排迁 action——
// 事实包收集/AI prompt/wiki.yaml 写入在 Actions.AnalyzeDomains）：
// cli 只做参数解析、ai 开关检查与输出。流程：结构化事实包 → AI 归纳
// 业务域 → 校验（防 AI 编造）→ 写回 wiki.yaml domains 区块（# AI 初稿
// → 人工确认契约）。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
	"gopkg.in/yaml.v3"
)

// cmdDomainsArgs 解析 `codeintel domains [--repo <path>] [--agent <a>]
// [--yaml <file>] [--json]` 参数。
func cmdDomainsArgs(args []string) int {
	f := queryFlags{}
	agent := ""
	yamlPath := ""
	factsPath := ""
	exportOnly := ""
	extraPrompt := "" // R57：用户约束（可预先指定部分域——wiki 生成前置 domains 已配置时提示用它）
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--repo" && i+1 < len(args):
			f.repoPath = args[i+1]
			i++
		case strings.HasPrefix(a, "--repo="):
			f.repoPath = strings.TrimPrefix(a, "--repo=")
		case a == "--agent" && i+1 < len(args):
			agent = args[i+1]
			i++
		case strings.HasPrefix(a, "--agent="):
			agent = strings.TrimPrefix(a, "--agent=")
		case a == "--yaml" && i+1 < len(args):
			yamlPath = args[i+1]
			i++
		case strings.HasPrefix(a, "--yaml="):
			yamlPath = strings.TrimPrefix(a, "--yaml=")
		case a == "--facts" && i+1 < len(args):
			factsPath = args[i+1]
			i++
		case strings.HasPrefix(a, "--facts="):
			factsPath = strings.TrimPrefix(a, "--facts=")
		case a == "--export-facts" && i+1 < len(args):
			exportOnly = args[i+1]
			i++
		case strings.HasPrefix(a, "--export-facts="):
			exportOnly = strings.TrimPrefix(a, "--export-facts=")
		case a == "--prompt" && i+1 < len(args):
			extraPrompt = args[i+1]
			i++
		case strings.HasPrefix(a, "--prompt="):
			extraPrompt = strings.TrimPrefix(a, "--prompt=")
		case a == "--json":
			f.json = true
		case a == "--help" || a == "-h":
			fmt.Println("用法: codeintel domains [--repo <path>] [--agent claude|codex|auto] [--yaml <file>] [--facts <file>] [--export-facts <file>] [--prompt <text>] [--json]\n  AI 业务域分析：静态事实包（包/表/实体/服务）导出文件 → agent 读文件归纳业务域 → 写回 wiki.yaml domains 区块（# AI 初稿 → 人工确认）——ER/实体分组统一消费；wiki 生成前置要求 domains 已配置（未配置时 wiki 拒绝生成）\n  --export-facts <file>：只导出事实包到文件（不调 AI，可人工检查/喂给任意 agent）；--prompt 用户约束（可预先指定部分域，如 \"商品域：交易域，会员域\"）")
			return 0
		default:
			fmt.Fprintf(os.Stderr, "error: 未知参数 %q\n", a)
			return 2
		}
	}
	abs, _, err := resolveRepo(f.repoPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if agent == "" {
		agent = "auto"
	}
	resolved, err := resolveAgent(agent)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return cmdDomains(abs, f, resolved, yamlPath, factsPath, exportOnly, extraPrompt)
}

// cmdDomains 实现 `codeintel domains [--repo <path>] [--agent claude|codex] [--json]`
func cmdDomains(repoAbs string, f queryFlags, agent, yamlPath, factsPath, exportOnly, extraPrompt string) int {
	db, err := sqlite.Open(repoAbs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()
	acts := action.New(sqlite.NewRepo(db))
	// 读现有 wiki.yaml（别名/既有 domains）
	cfg := wikiConfig{}
	if yamlPath == "" {
		yamlPath = filepath.Join(repoAbs, "wiki.yaml")
	}
	if b, err := os.ReadFile(yamlPath); err == nil {
		_ = yaml.Unmarshal(b, &cfg)
	}
	aliases := map[string]string{}
	for _, t := range cfg.Tables {
		aliases[t.Name] = t.Alias
	}
	req := action.DomainsRequest{
		RepoAbs:      repoAbs,
		Agent:        agent,
		YAMLPath:     yamlPath,
		FactsPath:    factsPath,
		ExportOnly:   exportOnly,
		ExtraPrompt:  extraPrompt,
		TableAliases: aliases,
		AgentRunner:  agentRunner,
	}
	// --export-facts：只导出事实包（不调 AI）
	if exportOnly != "" {
		res, err := acts.AnalyzeDomains(req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Printf("事实包已导出到 %s（%d 包、%d 表——可人工检查或喂给任意 agent）\n",
			exportOnly, len(res.Facts.Pkgs), len(res.Facts.Tables))
		return 0
	}
	// R56：ai.domains=off（wiki.yaml/全局）→ 整步跳过（不调 AI）
	if !aiEnabled("domains", cfg) {
		fmt.Fprintln(os.Stderr, "ai.domains=off——跳过业务域分析（wiki.yaml 或 ~/.codeintel/config.yaml 配置）")
		return 0
	}
	res, err := acts.AnalyzeDomains(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	for _, w := range res.Warns {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}
	if f.json {
		b, _ := json.MarshalIndent(res.Doms, "", "  ")
		fmt.Println(string(b))
		return 0
	}
	if len(res.Doms) == 0 {
		return 1
	}
	for _, d := range res.Doms {
		fmt.Printf("[%s] %s\n", d.Name, d.Description)
		fmt.Printf("  包: %s\n  表: %s\n", strings.Join(d.Packages, ", "), strings.Join(d.Tables, ", "))
	}
	fmt.Printf("\n共 %d 个业务域（已写回 %s，标注 # AI 初稿）\n", len(res.Doms), yamlPath)
	return 0
}
