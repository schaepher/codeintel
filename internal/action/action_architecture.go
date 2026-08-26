package action

// R9x 迁移：`query architecture` 结果装配（原 cli/query_architecture.go
// 的 architectureData）——模块数/业务域/架构图文本聚合；mermaid 文本
// 生成（yaml architecture 或 archLayeredMermaid fallback）与 plantuml
// 转换是渲染，留 cli（wiki_arch_layers.go/wiki_module.go）。

import (
	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// ArchitectureRequest query architecture 参数（配置加载在 cli 完成）。
type ArchitectureRequest struct {
	Data    []*domain.WikiModule // WikiData 结果（模块数据）
	Domains []string             // 配置的业务域（yaml domains 名称）
	Mermaid string               // 架构图文本（cli 渲染：yaml architecture 或自动 fallback）
}

// ArchitectureResult 架构图输出契约（cmd --json / MCP 共用）。
type ArchitectureResult struct {
	Modules int      `json:"modules"`           // 参与聚合的模块数
	Domains []string `json:"domains,omitempty"` // 配置的业务域（领域层聚合）
	Mermaid string   `json:"mermaid"`           // mermaid 文本（--format mermaid/plantuml 可再转）
}

// Architecture 装配架构图查询结果（模块数聚合 + 业务域 + 架构图文本；
// plantuml 转换等渲染留 cli）。
func (a *Actions) Architecture(req ArchitectureRequest) (*ArchitectureResult, error) {
	logger := zap.L()
	logger.Info("enter (Actions).Architecture", zap.Int("modules", len(req.Data)))
	defer logger.Info("exit (Actions).Architecture")
	out := &ArchitectureResult{Modules: len(req.Data), Domains: req.Domains, Mermaid: req.Mermaid}
	if out.Domains == nil {
		out.Domains = []string{}
	}
	return out, nil
}
