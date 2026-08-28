package action

// R34 `codeintel domains`——AI 业务域分析（批次 C 迁移，原 cli/domains.go
// 的编排）：结构化事实包（静态分析全算好——包清单/表清单/实体/服务/
// 调用聚合）→ AI 读文件归纳业务域（名称/描述/归属包与表）→ 校验（归属
// 须存在于事实包，防 AI 编造）→ 写回 wiki.yaml domains 区块（# AI 初稿
// → 人工确认契约）。cli 只留参数解析与输出。AI 调用经 Request 注入的
// AgentRunner（cli 的 agentRunner 可注入变量——测试替换点保持原样）。

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"
)

// WikiSubdomainCfg 域内子域（R80：AI 归纳——域过大时语义拆分；
// R100-2：按业务概念划分——不是按包/目录结构；包/表是承载业务概念
// 的载体，按概念归属）。cli 渲染层经类型别名消费。
type WikiSubdomainCfg struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Packages    []string `yaml:"packages"` // 承载该业务概念的包（归属校验）
	Tables      []string `yaml:"tables"`   // 承载该业务概念的表
}

// WikiDomainCfg 业务域（R34：AI 基于代码事实归纳——名称/描述/归属包与
// 表；静态分析（ER 领域分组/实体分组）统一消费此数据源，未覆盖的
// 包/表走前缀/DDD 目录规则降级）。cli 渲染层经类型别名消费。
type WikiDomainCfg struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Packages    []string `yaml:"packages"`
	Tables      []string `yaml:"tables"`
	// R38：归属服务名（grpc 服务名 / http "METHOD path"）——流程页
	// 服务子页按领域分目录的依据；AI 归纳 + 人工确认
	Services []string `yaml:"services"`
	// R80：AI 划分子域（域内语义子域——渲染分组（实体/ER 域内图）优先
	// 使用 subdomains 归属，未覆盖的包/表走自动细分降级）
	Subdomains []WikiSubdomainCfg `yaml:"subdomains"`
}

// DomainsRequest domains 分析参数（类型/格式合法性校验在 cli；配置
// 读取（wiki.yaml/全局 ai 开关）在 cli）。
type DomainsRequest struct {
	RepoAbs      string
	Agent        string // agent 名（claude|codex|auto——resolveAgent 在 cli）
	YAMLPath     string // wiki.yaml 路径（空 = 仓库根 wiki.yaml）
	FactsPath    string // 事实包导出路径（空 = 仓库 .codeintel/domain-facts.json）
	ExportOnly   string // 非空 = 只导出事实包到此路径（不调 AI，人工检查用）
	ExtraPrompt  string // 用户约束（R56 wiki --prompt——传入 DomainPrompt）
	TableAliases map[string]string
	AgentRunner  func(agent, prompt string, timeout time.Duration, dir string) (string, error)
}

// DomainsResult domains 分析结果（cli 输出：警告 + 域清单 / 导出计数）。
type DomainsResult struct {
	Doms  []WikiDomainCfg // 有效业务域（空 = 无结果，cli 按退出码 1）
	Warns []string        // 校验/降级警告
	Facts *DomainFacts    // 仅 --export-facts 场景返回（cli 打印包/表计数）
}

// AnalyzeDomains 核心流程：事实包导出文件 → AI 读文件归纳 → 解析校验
// → 写回 wiki.yaml。返回写回是否发生由 Doms 非空体现（无 domains 不
// 写）。factsPath 为空时写仓库 .codeintel/ 下。wiki 集成复用（已生成
// 跳过）。--export-facts：只导出事实包（不调 AI）。
func (a *Actions) AnalyzeDomains(req DomainsRequest) (*DomainsResult, error) {
	logger := zap.L()
	logger.Info("enter (Actions).AnalyzeDomains", zap.String("repo", req.RepoAbs), zap.String("agent", req.Agent))
	defer logger.Info("exit (Actions).AnalyzeDomains")
	res := &DomainsResult{}
	f := a.collectDomainFacts(DomainFactsRequest{RepoAbs: req.RepoAbs, TableAliases: req.TableAliases})

	// --export-facts：只导出事实包（不调 AI，缩进版可人工检查）
	if req.ExportOnly != "" {
		b, err := DomainFactsJSONIndent(f)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(req.ExportOnly, b, 0o644); err != nil {
			return nil, err
		}
		res.Facts = f
		return res, nil
	}
	if req.AgentRunner == nil {
		return nil, fmt.Errorf("未配置 agent runner（无法调用 AI）")
	}

	// R38：任务加重（读事实包 JSON + 归纳 packages/tables/services）——
	// 超时 240s → 360s（go2o 30 服务实测 4m 仍超）
	// R72：AI 把结果写入 .codeintel/domains-ai.json（文件是权威来源——
	// 响应文本解析失败/超时不影响）；超时后检查文件（不盲目完整重试）
	factsPath := req.FactsPath
	if factsPath == "" {
		factsPath = filepath.Join(req.RepoAbs, ".codeintel", "domain-facts.json")
	}
	if err := os.MkdirAll(filepath.Dir(factsPath), 0o755); err == nil {
		if b, err := DomainFactsJSON(f); err == nil {
			if err := os.WriteFile(factsPath, b, 0o644); err != nil {
				res.Warns = append(res.Warns, fmt.Sprintf("事实包写文件失败: %v", err))
				return res, nil
			}
		}
	}
	outPath := filepath.Join(req.RepoAbs, ".codeintel", "domains-ai.json")
	_ = os.Remove(outPath) // 清理旧结果（防读陈旧文件）
	resp, err := req.AgentRunner(req.Agent, DomainPrompt(factsPath, req.ExtraPrompt), 600*time.Second, req.RepoAbs)
	doms, warns := ParseDomains(resp, f)
	if len(doms) == 0 {
		// 响应无有效结果（含超时）——读 AI 写的 JSON 文件（超时但已
		// 写完 = AI 实际完成；文件是权威交付物）
		if b, rerr := os.ReadFile(outPath); rerr == nil && len(b) > 0 {
			if doms2, w2 := ParseDomains(string(b), f); len(doms2) > 0 {
				doms, warns = doms2, w2
			}
		}
	}
	if err != nil {
		if len(doms) > 0 {
			warns = append(warns, fmt.Sprintf("AI 响应异常（%v）——已用输出文件结果", err))
		} else {
			res.Warns = append(res.Warns, fmt.Sprintf("AI 业务域分析失败: %v（若 AI 进程仍残留可手动 kill；输出文件 %s 无结果）", err, outPath))
			return res, nil
		}
	}
	if len(doms) == 0 {
		warns = append(warns, "无有效业务域（保留现有规则划分）")
		res.Warns = warns
		return res, nil
	}
	// 写回 wiki.yaml（AI 初稿；未指定时用仓库根 wiki.yaml）
	yamlPath := req.YAMLPath
	if yamlPath == "" {
		yamlPath = filepath.Join(req.RepoAbs, "wiki.yaml")
	}
	if e, err := LoadYAMLEditor(yamlPath); err == nil {
		// R38：整体重归纳——先清旧 domains（setDomain 按名追加，
		// 域名变更会新旧并存）
		e.ClearDomains()
		for _, d := range doms {
			e.SetDomain(d)
		}
		if err := e.Save(yamlPath); err != nil {
			warns = append(warns, fmt.Sprintf("写回 %s: %v", yamlPath, err))
		}
	}
	res.Warns = warns
	res.Doms = doms
	return res, nil
}
