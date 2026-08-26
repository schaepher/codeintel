package action

// R9x 迁移：`export graph` 数据获取/编排（原 cli/export_graph.go 的
// cmdExportGraph 分发 switch）——callees/value-trace/lifecycle/modules
// 各型图数据获取 + 锚点解析 + 路径条件标注；mermaid/dot 文本拼装
// （渲染）留 cli（renderValueTraceMermaid 等）。

import (
	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// ExportGraphRequest export graph 参数（类型/格式合法性校验在 cli）。
type ExportGraphRequest struct {
	Type   string // value-trace | callees | lifecycle | modules
	Target string // 锚点输入（lifecycle 需 ResolveAnchor；modules 不需要）
}

// ExportGraphResult 图数据（渲染层按 Type 取用对应字段拼装文本）。
type ExportGraphResult struct {
	Type   string             // 实际类型
	Anchor domain.CanonicalID // 解析后锚点（lifecycle）
	Rows   []*domain.TraceRow // value-trace / lifecycle
	Facts  []*domain.Fact     // callees
	Calls  []ModuleCall       // modules（模块间调用）
}

// ExportGraph 获取导出图数据（Q89）：按类型分发到查询用例——callees
// 深度 1；value-trace 深度 8 全链（min-conf 0、不含容器——与 CLI 渲染
// 历史一致）；lifecycle 追加写锚点下游跳板 + 路径条件标注；modules
// 模块间调用聚合（无过滤）。
func (a *Actions) ExportGraph(req ExportGraphRequest) (*ExportGraphResult, error) {
	logger := zap.L()
	logger.Info("enter (Actions).ExportGraph", zap.String("type", req.Type), zap.String("target", req.Target))
	defer logger.Info("exit (Actions).ExportGraph")
	res := &ExportGraphResult{Type: req.Type}
	switch req.Type {
	case "callees":
		facts, err := a.Callees(domain.CanonicalID(req.Target), 1)
		if err != nil {
			return nil, err
		}
		res.Facts = facts
	case "modules":
		calls, err := a.ModuleCalls("")
		if err != nil {
			return nil, err
		}
		res.Calls = calls
	case "lifecycle":
		anchor, err := a.ResolveAnchor(req.Target)
		if err != nil {
			return nil, err
		}
		rows, err := a.Lifecycle(anchor)
		if err != nil {
			return nil, err
		}
		rows, err = a.TraceConditions(rows)
		if err != nil {
			return nil, err
		}
		res.Anchor, res.Rows = anchor, rows
	default: // value-trace（mermaid/dot 渲染共用同数据）
		rows, err := a.ValueTrace(domain.CanonicalID(req.Target), 8, 0, false)
		if err != nil {
			return nil, err
		}
		res.Rows = rows
	}
	return res, nil
}
