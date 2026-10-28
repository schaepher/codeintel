package action

// todo25 §13：observation 覆盖/行筛选（从 runtime_query.go 拆出）。
import (
	"errors"
	"fmt"
)

func runtimeObservationFor(snap *RuntimeSnapshot, q *runtimeQuery) (*runtimeObservation, error) {
	if snap == nil || q == nil {
		return nil, errors.New("没有快照/查询")
	}
	var matches []*runtimeObservation
	for _, obs := range snap.Observations {
		if obs.SourceID != q.SourceID || obs.Table != q.Table {
			continue
		}
		if !obs.Complete {
			continue
		}
		if !runtimeObservationColumns(obs, q) {
			continue
		}
		if !runtimeScopeCovers(obs.Scope, q.Predicates) {
			continue
		}
		matches = append(matches, obs)
	}
	switch len(matches) {
	case 0:
		return nil, errors.New("没有覆盖查询范围的完整 observation")
	case 1:
		return matches[0], nil
	default:
		return nil, errors.New("多个 observation 同时覆盖查询范围（不选第一项）")
	}
}

func runtimeObservationColumns(obs *runtimeObservation, q *runtimeQuery) bool {
	for _, c := range q.Columns {
		if !obs.Columns[c] {
			return false
		}
	}
	for _, p := range q.Predicates {
		if !obs.Columns[p.Field] {
			return false
		}
	}
	return true
}

// runtimeScopeCovers observation 的 scope 是否足以覆盖查询（T14 方向：
// **查询必须蕴含 observation 的每条限制**——observation 只对该 scope 完整）。
//   - 全表 observation（scope=[]）覆盖任意查询；
//   - observation(region=ID) 覆盖 query(region=ID, node=2)；
//   - observation(region=ID, node=2) 不覆盖 query(region=ID)（行被 node 过滤过）；
//   - observation 的每个谓词都要能在 query 里找到"同等或更窄"的谓词。
func runtimeScopeCovers(scope, query []runtimePredicate) bool {
	if len(scope) == 0 {
		return true // 全表 observation：行是完整的，可回答任意查询
	}
	if len(query) == 0 {
		return false // 查全表需要全表 observation
	}
	for _, op := range scope {
		var found *runtimePredicate
		for i := range query {
			if query[i].Field == op.Field {
				found = &query[i]
				break
			}
		}
		if found == nil {
			return false
		}
		if !runtimePredicateImplies(*found, op) {
			return false
		}
	}
	return true
}

// runtimePredicateImplies q 的取值集合是否被 observation 谓词 obs 完整包含
// （即 q 比 obs 更窄或相等）。
func runtimePredicateImplies(q, obs runtimePredicate) bool {
	switch q.Op {
	case "eq":
		switch obs.Op {
		case "eq":
			eq, known := rtEqual(q.Value, obs.Value)
			return known && eq
		case "in":
			return runtimeInContains(obs.Value, q.Value)
		}
	case "in":
		if q.Value.kind != rtSlice || len(q.Value.elems) == 0 {
			return false
		}
		switch obs.Op {
		case "eq":
			for _, qv := range q.Value.elems {
				eq, known := rtEqual(qv, obs.Value)
				if !known || !eq {
					return false
				}
			}
			return true
		case "in":
			for _, qv := range q.Value.elems {
				if !runtimeInContains(obs.Value, qv) {
					return false
				}
			}
			return true
		}
	}
	return false
}

func runtimeInContains(set, v rtValue) bool {
	if set.kind != rtSlice {
		return false
	}
	for _, e := range set.elems {
		if eq, known := rtEqual(e, v); known && eq {
			return true
		}
	}
	return false
}

// runtimeFilterRows 按查询谓词筛选 observation 行（完整范围内）。
func runtimeFilterRows(obs *runtimeObservation, q *runtimeQuery) ([]rtValue, error) {
	out := make([]rtValue, 0, len(obs.Rows))
	for _, row := range obs.Rows {
		ok := true
		for _, p := range q.Predicates {
			v, has := row.cols[p.Field]
			if !has {
				return nil, fmt.Errorf("行缺少过滤列 %q", p.Field)
			}
			switch p.Op {
			case "eq":
				if eq, known := rtEqual(v, p.Value); !known || !eq {
					ok = false
				}
			case "in":
				if !runtimeInContains(p.Value, v) {
					ok = false
				}
			}
			if !ok {
				break
			}
		}
		if ok {
			out = append(out, row)
		}
	}
	return out, nil
}

// runtimeResolveSource 从 binding 找 source id（精确 symbol；无 binding 返回空）。
func runtimeResolveSource(snap *RuntimeSnapshot, symbol string) string {
	if snap == nil {
		return ""
	}
	return snap.Bindings[symbol]
}
