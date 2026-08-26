package action

// R9x 迁移：`query module` 查询逻辑（原 cli/query_module.go 的
// moduleData + cli/wiki_keyflows.go 的 wikiModuleKeyFlows）——模块
// 详情装配：职责/入口/核心符号/关键数据流/模块间调用/相关表/包间
// 调用；模块架构图 mermaid（moduleArchMermaid）等渲染留 cli。

import (
	"fmt"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// ModuleRequest query module 参数。
type ModuleRequest struct {
	Data []*domain.WikiModule // WikiData 结果（模块列表）
	Name string               // 模块名（完整路径或短名）
}

// ModuleResult 模块详情输出契约（ArchMermaid 渲染留 cli 拼接）。
type ModuleResult struct {
	Name        string                `json:"name"`
	ShortName   string                `json:"short_name"`
	Desc        string                `json:"desc,omitempty"`
	Entries     []string              `json:"entries,omitempty"`
	CoreSymbols []string              `json:"core_symbols,omitempty"` // "Name (Kind, callers 调用者)" 展示
	KeyFlows    []WikiKeyFlow         `json:"key_flows,omitempty"`    // 核心符号字段读写
	OutCalls    []string              `json:"out_calls,omitempty"`
	InCalls     []string              `json:"in_calls,omitempty"`
	Tables      []string              `json:"tables,omitempty"`
	PkgCalls    []*domain.WikiPkgCall `json:"pkg_calls,omitempty"` // 包间调用（模块架构图数据）
}

// Module 模块详情（wiki 模块页同款：WikiData 结果 + 关键数据流）。
// 未找到模块返回 (nil, nil)。
func (a *Actions) Module(req ModuleRequest) (*ModuleResult, error) {
	logger := zap.L()
	logger.Info("enter (Actions).Module", zap.String("name", req.Name))
	defer logger.Info("exit (Actions).Module")
	var wm *domain.WikiModule
	for _, m := range req.Data {
		if m.Name == req.Name || m.ShortName == req.Name {
			wm = m
			break
		}
	}
	if wm == nil {
		return nil, nil
	}
	out := &ModuleResult{
		Name: wm.Name, ShortName: wm.ShortName, Desc: wm.Desc,
		Entries: wm.Entries, OutCalls: wm.OutCalls, InCalls: wm.InCalls,
		Tables: wm.Tables, PkgCalls: wm.PkgCalls,
	}
	for _, s := range wm.CoreSymbols {
		out.CoreSymbols = append(out.CoreSymbols, fmt.Sprintf("%s（%s，%d 调用者）", s.Name, s.Kind, s.Callers))
	}
	out.KeyFlows = a.wikiKeyFlowsOf(wm)
	return out, nil
}

// wikiKeyFlowsOf 模块核心符号的关键数据流（R17）：CoreSymbols 批量查
// 字段读写。用 canonical ID（名称跨包重名——ResolveSymbol 多匹配会
// 失败）。
func (a *Actions) wikiKeyFlowsOf(wm *domain.WikiModule) []WikiKeyFlow {
	var ids []string
	for _, s := range wm.CoreSymbols {
		if s.ID != "" {
			ids = append(ids, s.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return a.WikiKeyFlows(wm.Name, ids)
}
