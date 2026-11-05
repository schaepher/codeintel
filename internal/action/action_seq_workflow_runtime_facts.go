package action

// B08：运行时事实范围（从 action_seq_workflow_instance.go 拆出）。

import "strconv"

// B08：运行时事实范围（每个入口/实例接收同一不可变快照；实例读取值参与
// 配置表查询）。缺 scope/列/资源绑定属 missing_fact，静态不可分析属
// analysis_boundary，多候选冲突属 ambiguous_fact。

// seqWFRTValueLiteral rtValue → 规范化字面量（与 SeqStorageOrigin.Literal 同形）。
func seqWFRTValueLiteral(v rtValue) (string, bool) {
	switch v.kind {
	case rtString:
		return strconv.Quote(v.s), true
	case rtInt:
		return strconv.FormatInt(v.i, 10), true
	case rtFloat:
		return strconv.FormatFloat(v.f, 'g', -1, 64), true
	case rtBool:
		if v.b {
			return "true", true
		}
		return "false", true
	}
	return "", false
}

// seqWFRuntimeReadValues 用快照补足实例读取值（只补未知列；不覆盖写值）。
func seqWFRuntimeReadValues(snap *RuntimeSnapshot, reads []seqWFRead,
	readVals map[string]map[string]*SeqStorageOrigin) (int, int) {
	added, missing := 0, 0
	if snap == nil {
		return 0, 0
	}
	for _, r := range reads {
		if r.Table == "" || len(r.Preds) == 0 {
			continue
		}
		vals := readVals[r.ReadID]
		// 谓词值 → 字面量（可来自实例读取值）。
		preds := map[string]string{}
		ok := true
		for _, p := range r.Preds {
			lit, found := seqWFPredicateLiteral(p, readVals)
			if !found {
				ok = false
				break
			}
			preds[p.Column] = lit
		}
		if !ok {
			missing++
			continue
		}
		src := snap.Bindings[r.Resource]
		if src == "" {
			missing++
			continue
		}
		row, found, ambiguous := seqWFSnapshotRow(snap, src, r.Table, preds)
		if ambiguous {
			// 多候选冲突：不补值。
			missing++
			continue
		}
		if !found {
			missing++
			continue
		}
		// 补足该读取的列值（已有值不覆盖）。
		for _, col := range r.Columns {
			if vals != nil && vals[col] != nil {
				continue
			}
			rv, has := row[col]
			if !has {
				missing++
				continue
			}
			lit, ok := seqWFRTValueLiteral(rv)
			if !ok {
				missing++
				continue
			}
			if readVals[r.ReadID] == nil {
				readVals[r.ReadID] = map[string]*SeqStorageOrigin{}
			}
			scalar := "string"
			if rv.kind == rtInt {
				scalar = "int"
			}
			readVals[r.ReadID][col] = &SeqStorageOrigin{Kind: SeqOriginLiteral,
				ScalarType: scalar, Literal: lit}
			added++
		}
	}
	return added, missing
}

// seqWFPredicateLiteral 谓词值 → 字面量（read_column 用实例读取值；未知 → false）。
func seqWFPredicateLiteral(p SeqStoragePredicate, readVals map[string]map[string]*SeqStorageOrigin) (string, bool) {
	if p.Value.Origin == nil {
		return "", false
	}
	switch p.Value.Origin.Kind {
	case SeqOriginLiteral:
		return p.Value.Origin.Literal, true
	case SeqOriginReadColumn:
		rc := p.Value.Origin.ReadColumn
		if rc == nil || rc.Result == nil || rc.Result.Read == nil {
			return "", false
		}
		vals := readVals[rc.Result.Read.ID]
		if vals == nil || vals[rc.Column] == nil || vals[rc.Column].Kind != SeqOriginLiteral {
			return "", false
		}
		return vals[rc.Column].Literal, true
	}
	return "", false
}

// seqWFSnapshotRow 快照里按 scope 命中的唯一行（多行 → ambiguous）。
func seqWFSnapshotRow(snap *RuntimeSnapshot, sourceID, table string,
	preds map[string]string) (map[string]rtValue, bool, bool) {
	var rows []map[string]rtValue
	for _, obs := range snap.Observations {
		if obs.SourceID != sourceID || obs.Table != table || !obs.Complete {
			continue
		}
		if !seqWFObsScopeMatches(obs, preds) {
			continue
		}
		for _, rv := range obs.Rows {
			if rv.kind != rtRow {
				continue
			}
			rows = append(rows, rv.cols)
		}
	}
	if len(rows) == 1 {
		return rows[0], true, false
	}
	if len(rows) > 1 {
		// 相同规范化 scope 的多条观测/多行：不能拼接（ambiguous_fact）。
		return nil, false, true
	}
	return nil, false, false
}

// seqWFObsScopeMatches 快照 scope 是否与查询谓词一致（列名 + 值）。
func seqWFObsScopeMatches(obs *runtimeObservation, preds map[string]string) bool {
	if obs == nil || len(obs.Scope) != len(preds) {
		return false
	}
	for _, p := range obs.Scope {
		lit, ok := seqWFRTValueLiteral(p.Value)
		if !ok {
			return false
		}
		if preds[p.Field] != lit {
			return false
		}
	}
	return true
}
