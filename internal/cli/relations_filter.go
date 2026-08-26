package cli

// 批次 C：relations 输出过滤（--type/--max-hops/--max-results）迁
// action（Actions.RelationsQuery 内 filterRelations）；本文件只留
// flag → RelationHops 组装（参数解析）。

import (
	"github.com/schaepher/codeintel/internal/domain"
)

// relationHopsFromFlags 组装三类跳数上限（Q197）：默认 4；
// 显式传 0 = 不限制；--include-long-query 等价 --query-max-hops 0。
func relationHopsFromFlags(f *queryFlags) domain.RelationHops {
	h := domain.DefaultRelationHops

	if f.queryMaxHops >= 0 {
		h.Query = f.queryMaxHops
	}
	if f.writeMaxHops >= 0 {
		h.Write = f.writeMaxHops
	}
	if f.readMaxHops >= 0 {
		h.Read = f.readMaxHops
	}
	if f.includeLongQuery {
		h.Query = 0
	}
	return h
}
