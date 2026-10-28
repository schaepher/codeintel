package action

// todo25 §12：async 候选预算分配（关系强度优先）与查询内符号复用。

import (
	"sort"

	"github.com/schaepher/codeintel/internal/domain"
)

// asyncSelectCandidates 按关系强度选候选并截断；返回（预算内 ID、reason、
// 截断前总数）。seed 不在候选集合内。
func asyncSelectCandidates(a *Actions, seed *domain.CodeEntity, facts map[string]*asyncFnFacts,
	seedFields, seedTypePrefixes, idColumns map[string]bool) ([]string, map[string]string, int) {
	const (
		asyncPriDirect = iota
		asyncPriField
		asyncPriType
		asyncPriCallPath
		asyncPriIDName
	)
	candPri := map[string]int{}
	candWhy := map[string]string{}
	addCand := func(id, why string, prio int) {
		if seed != nil && id == string(seed.ID) {
			return
		}
		if old, ok := candPri[id]; ok && old <= prio {
			return
		}
		candPri[id] = prio
		candWhy[id] = why
	}
	if seed != nil {
		for id := range a.directCalleesOf(string(seed.ID)) {
			addCand(id, "direct_call", asyncPriDirect)
		}
		for id := range a.directCallersOf(string(seed.ID)) {
			addCand(id, "direct_call", asyncPriDirect)
		}
	}
	if len(seedFields) > 0 {
		for id, f := range facts {
			if seed != nil && id == string(seed.ID) {
				continue
			}
			best, why := asyncPriIDName+1, ""
			for _, fields := range []map[string]bool{f.writes, f.reads} {
				for p := range fields {
					switch {
					case seedFields[p] && asyncPriField < best:
						best, why = asyncPriField, "field_identity"
					case !seedFields[p] && asyncSharesType(p, seedTypePrefixes) && asyncPriType < best:
						best, why = asyncPriType, "type_scope"
					case !seedFields[p] && !asyncSharesType(p, seedTypePrefixes) &&
						asyncSharesIDColumn(p, idColumns) && asyncPriIDName < best:
						best, why = asyncPriIDName, "id_name"
					}
				}
			}
			if why != "" {
				addCand(id, why, best)
			}
		}
	}
	if seed != nil {
		for id := range chainSymbols(a, string(seed.ID)) {
			addCand(id, "call_path", asyncPriCallPath)
		}
	}
	ids := make([]string, 0, len(candPri))
	for id := range candPri {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if candPri[ids[i]] != candPri[ids[j]] {
			return candPri[ids[i]] < candPri[ids[j]]
		}
		return ids[i] < ids[j]
	})
	total := len(ids)
	if total > asyncCandidateBudget {
		ids = ids[:asyncCandidateBudget]
	}
	return ids, candWhy, total
}

// asyncSymbolCache 查询内符号缓存（GetSymbol 唯一入口；错误也缓存）。
type asyncSymbolCache struct {
	Reader
	cache map[string]asyncSymEntry
}

type asyncSymEntry struct {
	node *domain.CodeEntity
	err  error
}

func (c *asyncSymbolCache) GetSymbol(id domain.CanonicalID) (*domain.CodeEntity, error) {
	if e, hit := c.cache[string(id)]; hit {
		return e.node, e.err
	}
	n, err := c.Reader.GetSymbol(id)
	c.cache[string(id)] = asyncSymEntry{node: n, err: err}
	return n, err
}
