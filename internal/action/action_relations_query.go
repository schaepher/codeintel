package action

// 批次 C 迁移：`query relations` 的查询/过滤逻辑（原 cli/relations_query.go
// 的 SetRelationHops+Relations 编排 与 cli/relations_filter.go 的过滤）——
// 迁 action 后 cli 只做参数解析与输出（mermaid/json/文本渲染留 cli）。

import (
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// RelationsQueryRequest query relations 参数（flag 解析与 hops 组装在
// cli——relationHopsFromFlags；此处收最终值）。
type RelationsQueryRequest struct {
	Table      string              // 表名（--all 时忽略）
	MemoryMode string              // --memory full|sql（"" = auto 按规模）
	Hops       domain.RelationHops // 三类跳数上限（--query-max-hops 等，Q197）
	Types      []string            // --type 过滤（空 = 默认 fk+query+write）
	MaxHops    int                 // --max-hops 过滤（0 = 不限制）
	MaxResults int                 // --max-results 截断（0 = 不限制）
}

// RelationsQuery 单表关联查询 + 输出过滤（P0④）：表名 → 沿数据流链
// 关联的其他表.列（代码层推断，无外键依赖）。
func (a *Actions) RelationsQuery(req RelationsQueryRequest) ([]*domain.TableRelation, error) {
	logger := zap.L()
	logger.Info("enter (Actions).RelationsQuery", zap.String("table", req.Table), zap.String("memory_mode", req.MemoryMode))
	defer logger.Info("exit (Actions).RelationsQuery")
	a.SetRelationHops(req.Hops)
	rels, err := a.Relations(req.Table, req.MemoryMode)
	if err != nil {
		return nil, err
	}
	return filterRelations(rels, req.Types, req.MaxHops, req.MaxResults), nil
}

// RelationsAllQuery 全库表间关联聚合（query relations --all，Q160）：
// 一次遍历全部表返回所有表对关联（合并去重）+ 输出过滤。
func (a *Actions) RelationsAllQuery(req RelationsQueryRequest) ([]*domain.TableRelation, error) {
	logger := zap.L()
	logger.Info("enter (Actions).RelationsAllQuery", zap.String("memory_mode", req.MemoryMode))
	defer logger.Info("exit (Actions).RelationsAllQuery")
	a.SetRelationHops(req.Hops)
	rels, err := a.RelationsAll(req.MemoryMode)
	if err != nil {
		return nil, err
	}
	return filterRelations(rels, req.Types, req.MaxHops, req.MaxResults), nil
}

// filterRelations 输出过滤（--type/--max-hops/--max-results，P0④）。
// 默认类型：query + write + fk（read 低置信间接扩散，--type read 显式
// 展开）。
func filterRelations(rels []*domain.TableRelation, typeArgs []string, maxHops, maxResults int) []*domain.TableRelation {
	types := map[string]bool{}
	for _, t := range typeArgs {
		if t = strings.TrimSpace(t); t != "" {
			types[t] = true
		}
	}
	if len(types) == 0 {
		types[string(domain.RelationFK)] = true
		types[string(domain.RelationQuery)] = true
		types[string(domain.RelationWrite)] = true
	}
	out := make([]*domain.TableRelation, 0, len(rels))
	for _, r := range rels {
		if !types[string(r.Type)] {
			continue
		}
		if maxHops > 0 && r.Hops > maxHops {
			continue
		}
		out = append(out, r)
	}
	if maxResults > 0 && len(out) > maxResults {
		out = out[:maxResults]
	}
	return out
}
