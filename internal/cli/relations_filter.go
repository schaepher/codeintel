package cli

import (
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// relationsFilter P0④ 输出过滤：--type/--max-hops/--max-results。
// 默认类型：query + write（read 低置信间接扩散，--type read 显式展开）。
func relationsFilter(f *queryFlags) func([]*domain.TableRelation) []*domain.TableRelation {
	types := map[string]bool{}
	for _, t := range f.relTypes {
		if t = strings.TrimSpace(t); t != "" {
			types[t] = true
		}
	}
	if len(types) == 0 {

		types[domain.RelationFK] = true
		types[domain.RelationQuery] = true
		types[domain.RelationWrite] = true
	}
	return func(rels []*domain.TableRelation) []*domain.TableRelation {
		out := make([]*domain.TableRelation, 0, len(rels))
		for _, r := range rels {
			if !types[r.Type] {
				continue
			}
			if f.maxHops > 0 && r.Hops > f.maxHops {
				continue
			}
			out = append(out, r)
		}
		if f.maxResults > 0 && len(out) > f.maxResults {
			out = out[:f.maxResults]
		}
		return out
	}
}

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
