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

// runtimeScopeCovers observation 的 scope 是否覆盖查询谓词（查询更窄/相等）。
func runtimeScopeCovers(scope, query []runtimePredicate) bool {
	if len(query) == 0 {
		return len(scope) == 0 // 只有全表 observation 能覆盖"查全表"
	}
	for _, qp := range query {
		var found *runtimePredicate
		for i := range scope {
			if scope[i].Field == qp.Field {
				found = &scope[i]
				break
			}
		}
		if found == nil {
			return false
		}
		if !runtimePredicateCovers(*found, qp) {
			return false
		}
	}
	return true
}

func runtimePredicateCovers(obs, q runtimePredicate) bool {
	switch q.Op {
	case "eq":
		switch obs.Op {
		case "eq":
			eq, known := rtEqual(obs.Value, q.Value)
			return known && eq
		case "in":
			return runtimeInContains(obs.Value, q.Value)
		}
	case "in":
		if obs.Op != "in" || q.Value.kind != rtSlice || obs.Value.kind != rtSlice {
			return false
		}
		for _, qv := range q.Value.elems {
			if !runtimeInContains(obs.Value, qv) {
				return false
			}
		}
		return true
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
