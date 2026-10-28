package action

// todo25 §13.1：v2 快照解析（通用包络 + database observation）。

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

type runtimeV2Source struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
}

type runtimeV2Binding struct {
	Target struct {
		Symbol string `json:"symbol"`
	} `json:"target"`
	Source string `json:"source"`
}

type runtimeV2Predicate struct {
	Field string          `json:"field"`
	Op    string          `json:"op"`
	Value json.RawMessage `json:"value"`
}

type runtimeV2Observation struct {
	Source   string `json:"source"`
	Resource struct {
		Table string `json:"table"`
	} `json:"resource"`
	Scope    []runtimeV2Predicate `json:"scope"`
	Coverage struct {
		Complete *bool    `json:"complete"`
		Columns  []string `json:"columns"`
	} `json:"coverage"`
	Rows       []map[string]json.RawMessage `json:"rows"`
	Provenance struct {
		ObservedAt string `json:"observed_at"`
		Revision   string `json:"revision"`
	} `json:"provenance"`
}

type runtimeV2Doc struct {
	Version      int                        `json:"version"`
	Context      map[string]json.RawMessage `json:"context"`
	Sources      []runtimeV2Source          `json:"sources"`
	Bindings     []runtimeV2Binding         `json:"bindings"`
	Observations []runtimeV2Observation     `json:"observations"`
}

func parseRuntimeV2(data []byte) (*RuntimeSnapshot, error) {
	var doc runtimeV2Doc
	if err := decodeStrictJSON(data, &doc); err != nil {
		return nil, fmt.Errorf("v2 快照结构无效: %w", err)
	}
	snap := &RuntimeSnapshot{Version: 2, Context: map[string]rtValue{}, Sources: map[string]string{},
		Bindings: map[string]string{}}
	for k, raw := range doc.Context {
		v, err := decodeJSONValue(raw)
		if err != nil {
			return nil, fmt.Errorf("v2 context.%s: %w", k, err)
		}
		snap.Context[k] = v
	}
	for _, src := range doc.Sources {
		if src.ID == "" {
			return nil, errors.New("v2 source.id 为空")
		}
		if _, dup := snap.Sources[src.ID]; dup {
			return nil, fmt.Errorf("v2 重复 source id %q", src.ID)
		}
		if src.Kind != "database" {
			return nil, fmt.Errorf("v2 source %q 的 kind %q 不支持（首期只支持 database）", src.ID, src.Kind)
		}
		snap.Sources[src.ID] = src.Kind
	}
	bindSeen := map[string]bool{}
	for _, b := range doc.Bindings {
		sym := b.Target.Symbol
		if bindSeen[sym] {
			return nil, fmt.Errorf("v2 重复 binding target %q", sym)
		}
		bindSeen[sym] = true
		snap.Bindings[sym] = b.Source
	}
	totalRows := 0
	for i, obs := range doc.Observations {
		parsed, err := parseRuntimeV2Observation(obs)
		if err != nil {
			return nil, fmt.Errorf("v2 observation[%d]: %w", i, err)
		}
		totalRows += len(parsed.Rows)
		if totalRows > runtimeSnapshotMaxRows {
			return nil, fmt.Errorf("运行时快照总行数超过 %d 限制", runtimeSnapshotMaxRows)
		}
		snap.Observations = append(snap.Observations, parsed)
	}
	return snap, nil
}

func parseRuntimeV2Observation(o runtimeV2Observation) (*runtimeObservation, error) {
	if o.Source == "" {
		return nil, errors.New("source 为空")
	}
	if o.Resource.Table == "" {
		return nil, errors.New("resource.table 为空")
	}
	if o.Coverage.Complete == nil {
		return nil, errors.New("coverage.complete 缺失（不能用零值当完整/不完整）")
	}
	cols := map[string]bool{}
	for _, c := range o.Coverage.Columns {
		if c == "" {
			return nil, errors.New("coverage.columns 含空列名")
		}
		if cols[c] {
			return nil, fmt.Errorf("coverage.columns 重复列 %q", c)
		}
		cols[c] = true
	}
	scope, err := parseRuntimeV2Scope(o.Scope)
	if err != nil {
		return nil, err
	}
	for _, p := range scope {
		if !cols[p.Field] {
			return nil, fmt.Errorf("scope 字段 %q 未在 coverage.columns 声明", p.Field)
		}
	}
	if o.Rows == nil {
		return nil, errors.New("rows 缺失或为 null（完整空结果必须显式 rows:[]）")
	}
	rows, rowCols, err := decodeJSONRows(o.Rows)
	if err != nil {
		return nil, err
	}
	if err := validateRuntimeRows(rows, cols); err != nil {
		return nil, err
	}
	_ = rowCols
	if !runtimeTimeValid(o.Provenance.ObservedAt) {
		return nil, fmt.Errorf("provenance.observed_at 不是合法 RFC3339: %q", o.Provenance.ObservedAt)
	}
	return &runtimeObservation{
		SourceID: o.Source, Table: o.Resource.Table, Scope: scope,
		Complete: *o.Coverage.Complete, Columns: cols, Rows: rows,
		ObservedAt: o.Provenance.ObservedAt, Revision: o.Provenance.Revision,
	}, nil
}

func parseRuntimeV2Scope(in []runtimeV2Predicate) ([]runtimePredicate, error) {
	out := make([]runtimePredicate, 0, len(in))
	for i, p := range in {
		if p.Field == "" {
			return nil, fmt.Errorf("scope[%d].field 为空", i)
		}
		v, err := decodeJSONValue(p.Value)
		if err != nil {
			return nil, fmt.Errorf("scope[%d].value: %w", i, err)
		}
		switch p.Op {
		case "eq":
			if v.kind == rtSlice {
				return nil, fmt.Errorf("scope[%d] eq 的值不能是数组", i)
			}
		case "in":
			if v.kind != rtSlice || len(v.elems) == 0 {
				return nil, fmt.Errorf("scope[%d] in 的值必须是非空数组", i)
			}
			for _, e := range v.elems {
				if e.kind == rtSlice || e.kind == rtRow || e.kind == rtRows {
					return nil, fmt.Errorf("scope[%d] in 数组元素必须是标量", i)
				}
			}
		default:
			return nil, fmt.Errorf("scope[%d] 不支持的 op %q（只支持 eq/in）", i, p.Op)
		}
		out = append(out, runtimePredicate{Field: p.Field, Op: p.Op, Value: v})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Field < out[j].Field })
	return out, nil
}

// validateRuntimeRows：每行必须恰好声明列；类型按列一致（null 允许）。
func validateRuntimeRows(rows []rtValue, cols map[string]bool) error {
	kinds := map[string]rtKind{}
	for i, row := range rows {
		if row.kind != rtRow {
			return fmt.Errorf("第 %d 行不是对象", i+1)
		}
		if len(row.cols) != len(cols) {
			return fmt.Errorf("第 %d 行列集合与 coverage.columns 不一致（缺列或额外列）", i+1)
		}
		for c := range cols {
			v, ok := row.cols[c]
			if !ok {
				return fmt.Errorf("第 %d 行缺少声明列 %q", i+1, c)
			}
			if v.kind == rtNull {
				continue
			}
			if prev, ok := kinds[c]; ok && prev != v.kind {
				return fmt.Errorf("第 %d 行列 %q 类型不兼容（%v vs %v）", i+1, c, prev, v.kind)
			}
			kinds[c] = v.kind
		}
	}
	return nil
}
