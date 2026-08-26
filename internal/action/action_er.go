package action

// R9x 迁移：`query er` / wiki ER 页面的表间关系获取（原 cli/wiki_relations.go
// 的 wikiRelations 编排）——全库表间关联 + 未算（ErrRelationInProgress）
// 时同步兜底计算。cli/wiki 是批处理命令直接等结果；serve 的异步兜底
// 不适合。mermaid/plantuml 渲染与隐藏表过滤留 cli（renderERMermaid/
// mermaidToPlantuml/hideTableFrom）。

import (
	"errors"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// ERRelations 全库表间关联（Q251/R77 `query er` 与 wiki ER 页数据源）：
// 优先复用已算 relation_candidates；未算（ErrRelationInProgress）时同步
// 兜底计算——此前 cli 层 wikiRelations 的编排（查询逻辑）迁入 action，
// cli/wiki/MCP 同源调用。
func (a *Actions) ERRelations() ([]*domain.TableRelation, error) {
	logger := zap.L()
	logger.Info("enter (Actions).ERRelations")
	defer logger.Info("exit (Actions).ERRelations")
	rels, err := a.RelationsAll("")
	if err == nil {
		return rels, nil
	}
	if !errors.Is(err, domain.ErrRelationInProgress) {
		return nil, err
	}
	logger.Info("ER 图关系未计算——同步触发全量计算（首次生成需要等待）")
	if err := a.PrecomputeAllRelations(nil); err != nil {
		return nil, err
	}
	return a.RelationsAll("")
}
