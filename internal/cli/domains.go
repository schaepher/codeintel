package cli

// R34 `codeintel domains`——AI 业务域分析（用户要求：AI 介入前提供
// 足够信息避免误判）。流程：结构化事实包（静态分析全算好——包清单/
// 表清单/实体/服务/调用聚合）→ AI 归纳业务域（名称/描述/归属包与表）
// → 校验（归属须存在于事实包，防 AI 编造）→ 写回 wiki.yaml domains
// 区块（# AI 初稿 → 人工确认契约）。wiki 内部调用——已生成过（yaml
// 有 domains）就不再生成。静态分析（ER/实体分组）统一消费此数据源。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
	"gopkg.in/yaml.v3"
)

// parseDomains 解析 AI 返回的 domains YAML + 校验（归属须在事实包中，
// 防 AI 编造——校验失败剔除并警告）。
func parseDomains(resp string, f *domainFacts) ([]wikiDomainCfg, []string) {
	var out struct {
		Domains []wikiDomainCfg `yaml:"domains"`
	}
	if err := yaml.Unmarshal([]byte(resp), &out); err != nil {
		// 宽松重试（可能带 ```yaml 围栏）
		s := stripYAMLFence(resp)
		if err2 := yaml.Unmarshal([]byte(s), &out); err2 != nil {
			return nil, []string{fmt.Sprintf("domains 解析失败: %v", err2)}
		}
	}
	havePkg := map[string]bool{}
	haveTbl := map[string]bool{}
	haveSvc := map[string]bool{}
	for _, p := range f.Pkgs {
		havePkg[p.Path] = true // 完整路径校验（AI 输出完整路径）
	}
	for _, t := range f.Tables {
		haveTbl[t.Name] = true
	}
	for _, s := range f.Svcs {
		haveSvc[s.Name] = true
	}
	var doms []wikiDomainCfg
	var warns []string
	for _, d := range out.Domains {
		if d.Name == "" {
			warns = append(warns, "跳过无名称的域")
			continue
		}
		var pkgs, tbls, svcs []string
		for _, p := range d.Packages {
			if havePkg[p] {
				pkgs = append(pkgs, p)
			} else {
				warns = append(warns, fmt.Sprintf("域 %s：包 %s 不在事实包中（剔除）", d.Name, p))
			}
		}
		for _, t := range d.Tables {
			if haveTbl[t] {
				tbls = append(tbls, t)
			} else {
				warns = append(warns, fmt.Sprintf("域 %s：表 %s 不在事实包中（剔除）", d.Name, t))
			}
		}
		// R38：services 校验（服务名须在事实包 services 名单）
		for _, s := range d.Services {
			if haveSvc[s] {
				svcs = append(svcs, s)
			} else {
				warns = append(warns, fmt.Sprintf("域 %s：服务 %s 不在事实包中（剔除）", d.Name, s))
			}
		}
		if len(pkgs) == 0 && len(tbls) == 0 {
			warns = append(warns, fmt.Sprintf("域 %s：无有效归属（剔除）", d.Name))
			continue
		}
		d.Packages, d.Tables, d.Services = pkgs, tbls, svcs
		doms = append(doms, d)
	}
	return doms, warns
}

// analyzeDomains 核心流程：事实包导出文件 → AI 读文件归纳 → 解析校验
// → 写回 wiki.yaml。返回写回是否发生（无 domains 不写）。wiki 集成
// 复用（已生成跳过）。factsPath 为空时写仓库 .codeintel/ 下。
func analyzeDomains(repoAbs string, cfg *wikiConfig, acts *action.Actions, db *sqlite.Repo, agent string, yamlPath string, factsPath string) ([]wikiDomainCfg, []string) {
	f := collectDomainFacts(acts, repoAbs, *cfg, db)
	if factsPath == "" {
		factsPath = filepath.Join(repoAbs, ".codeintel", "domain-facts.txt")
	}
	if err := os.MkdirAll(filepath.Dir(factsPath), 0o755); err == nil {
		if b, err := domainFactsJSON(f); err == nil {
			if err := os.WriteFile(factsPath, b, 0o644); err != nil {
				return nil, []string{fmt.Sprintf("事实包写文件失败: %v", err)}
			}
		}
	}
	// R38：任务加重（读事实包 JSON + 归纳 packages/tables/services）——
	// 超时 240s → 360s（go2o 30 服务实测 4m 仍超）
	resp, err := agentRunner(agent, domainPrompt(factsPath), 360*time.Second, repoAbs)
	if err != nil {
		return nil, []string{fmt.Sprintf("AI 业务域分析失败: %v", err)}
	}
	doms, warns := parseDomains(resp, f)
	if len(doms) == 0 {
		warns = append(warns, "无有效业务域（保留现有规则划分）")
		return nil, warns
	}
	// 写回 wiki.yaml（AI 初稿；未指定时用仓库根 wiki.yaml）
	if yamlPath == "" {
		yamlPath = filepath.Join(repoAbs, "wiki.yaml")
	}
	if e, err := loadYAMLEditor(yamlPath); err == nil {
		// R38：整体重归纳——先清旧 domains（setDomain 按名追加，
		// 域名变更会新旧并存）
		e.clearDomains()
		for _, d := range doms {
			e.setDomain(d)
		}
		if err := e.save(yamlPath); err != nil {
			warns = append(warns, fmt.Sprintf("写回 %s: %v", yamlPath, err))
		}
	}
	cfg.Domains = doms
	return doms, warns
}

// cmdDomainsArgs 解析 `codeintel domains [--repo <path>] [--agent <a>]
// [--yaml <file>] [--json]` 参数。
func cmdDomainsArgs(args []string) int {
	f := queryFlags{}
	agent := ""
	yamlPath := ""
	factsPath := ""
	exportOnly := ""
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
		case a == "--json":
			f.json = true
		case a == "--help" || a == "-h":
			fmt.Println("用法: codeintel domains [--repo <path>] [--agent claude|codex|auto] [--yaml <file>] [--facts <file>] [--export-facts <file>] [--json]\n  AI 业务域分析：静态事实包（包/表/实体/服务）导出文件 → agent 读文件归纳业务域 → 写回 wiki.yaml domains 区块（# AI 初稿 → 人工确认）——ER/实体分组统一消费\n  --export-facts <file>：只导出事实包到文件（不调 AI，可人工检查/喂给任意 agent）")
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
	return cmdDomains(abs, f, resolved, yamlPath, factsPath, exportOnly)
}

// cmdDomains 实现 `codeintel domains [--repo <path>] [--agent claude|codex] [--json]`
func cmdDomains(repoAbs string, f queryFlags, agent, yamlPath, factsPath, exportOnly string) int {
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
	// --export-facts：只导出事实包（不调 AI）
	if exportOnly != "" {
		f := collectDomainFacts(acts, repoAbs, cfg, sqlite.NewRepo(db))
		b, err := domainFactsJSON(f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		if err := os.WriteFile(exportOnly, b, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Printf("事实包已导出到 %s（%d 包、%d 表——可人工检查或喂给任意 agent）\n",
			exportOnly, len(f.Pkgs), len(f.Tables))
		return 0
	}
	doms, warns := analyzeDomains(repoAbs, &cfg, acts, sqlite.NewRepo(db), agent, yamlPath, factsPath)
	for _, w := range warns {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}
	if f.json {
		b, _ := json.MarshalIndent(doms, "", "  ")
		fmt.Println(string(b))
		return 0
	}
	if len(doms) == 0 {
		return 1
	}
	for _, d := range doms {
		fmt.Printf("[%s] %s\n", d.Name, d.Description)
		fmt.Printf("  包: %s\n  表: %s\n", strings.Join(d.Packages, ", "), strings.Join(d.Tables, ", "))
	}
	fmt.Printf("\n共 %d 个业务域（已写回 %s，标注 # AI 初稿）\n", len(doms), yamlPath)
	return 0
}
