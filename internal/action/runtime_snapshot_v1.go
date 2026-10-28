package action

// todo25 §13.1：v1 快照解析——规范化到与 v2 相同的内部事实模型（不静默
// 把旧格式解释为 v2）。

import (
	"errors"
	"fmt"
	"sort"
)

type runtimeV1Binding struct {
	Symbol   string `json:"symbol"`
	Database string `json:"database"`
}

type runtimeV1Table struct {
	Name     string         `json:"name"`
	Scope    map[string]any `json:"scope"`
	Complete bool           `json:"complete"`
	Rows     []map[string]any `json:"rows"`
}

type runtimeV1Database struct {
	ID     string           `json:"id"`
	Tables []runtimeV1Table `json:"tables"`
}

type runtimeV1Doc struct {
	Version    int                `json:"version"`
	Environment string            `json:"environment"`
	Region     string            `json:"region"`
	CapturedAt string            `json:"captured_at"`
	Bindings   []runtimeV1Binding `json:"bindings"`
	Databases  []runtimeV1Database `json:"databases"`
}

func parseRuntimeV1(data []byte) (*RuntimeSnapshot, error) {
	var doc runtimeV1Doc
	if err := decodeStrictJSON(data, &doc); err != nil {
		return nil, fmt.Errorf("v1 快照结构无效: %w", err)
	}
	if !runtimeTimeValid(doc.CapturedAt) {
		return nil, fmt.Errorf("v1 captured_at 不是合法 RFC3339: %q", doc.CapturedAt)
	}
	snap := &RuntimeSnapshot{Version: 1, Context: map[string]rtValue{}, Sources: map[string]string{},
		Bindings: map[string]string{}}
	if doc.Environment != "" {
		snap.Context["environment"] = rtStringValue(doc.Environment)
	}
	if doc.Region != "" {
		snap.Context["region"] = rtStringValue(doc.Region)
	}
	bindSeen := map[string]bool{}
	for _, b := range doc.Bindings {
		if b.Symbol == "" {
			return nil, errors.New("v1 binding.symbol 为空")
		}
		if bindSeen[b.Symbol] {
			return nil, fmt.Errorf("v1 重复 binding symbol %q", b.Symbol)
		}
		bindSeen[b.Symbol] = true
		if b.Database == "" {
			return nil, fmt.Errorf("v1 binding %s 的 database 为空", b.Symbol)
		}
		snap.Bindings[b.Symbol] = b.Database
	}
	totalRows := 0
	for _, db := range doc.Databases {
		if db.ID == "" {
			return nil, errors.New("v1 database.id 为空")
		}
		if _, dup := snap.Sources[db.ID]; dup {
			return nil, fmt.Errorf("v1 重复 database id %q", db.ID)
		}
		snap.Sources[db.ID] = "database"
		for _, tbl := range db.Tables {
			obs, n, err := parseRuntimeV1Table(db.ID, tbl, doc.CapturedAt)
			if err != nil {
				return nil, fmt.Errorf("v1 database %s table %s: %w", db.ID, tbl.Name, err)
			}
			totalRows += n
			if totalRows > runtimeSnapshotMaxRows {
				return nil, fmt.Errorf("运行时快照总行数超过 %d 限制", runtimeSnapshotMaxRows)
			}
			snap.Observations = append(snap.Observations, obs)
		}
	}
	return snap, nil
}

func parseRuntimeV1Table(dbID string, tbl runtimeV1Table, capturedAt string) (*runtimeObservation, int, error) {
	if tbl.Name == "" {
		return nil, 0, errors.New("table.name 为空")
	}
	fields := make([]string, 0, len(tbl.Scope))
	for k := range tbl.Scope {
		fields = append(fields, k)
	}
	sort.Strings(fields)
	scope := make([]runtimePredicate, 0, len(fields))
	cols := map[string]bool{}
	for _, k := range fields {
		if k == "" {
			return nil, 0, errors.New("scope 含空字段名")
		}
		v, err := rtFromJSON(tbl.Scope[k])
		if err != nil {
			return nil, 0, fmt.Errorf("scope.%s: %w", k, err)
		}
		if v.kind == rtSlice || v.kind == rtRow || v.kind == rtRows {
			return nil, 0, fmt.Errorf("v1 scope.%s 必须是标量", k)
		}
		scope = append(scope, runtimePredicate{Field: k, Op: "eq", Value: v})
		cols[k] = true
	}
	if tbl.Complete && tbl.Rows == nil {
		return nil, 0, errors.New("rows 缺失或为 null 不能当完整空结果（完整空结果显式 rows:[]）")
	}
	rows := make([]rtValue, 0, len(tbl.Rows))
	for i, raw := range tbl.Rows {
		row, err := rtRowFromMap(raw)
		if err != nil {
			return nil, 0, fmt.Errorf("第 %d 行: %w", i+1, err)
		}
		for c := range row.cols {
			cols[c] = true
		}
		rows = append(rows, row)
	}
	if err := validateRuntimeRows(rows, cols); err != nil {
		return nil, 0, err
	}
	return &runtimeObservation{
		SourceID: dbID, Table: tbl.Name, Scope: scope, Complete: tbl.Complete,
		Columns: cols, Rows: rows, ObservedAt: capturedAt,
	}, len(rows), nil
}
