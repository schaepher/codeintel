package sqlite

// P1-11：函数内查询的前缀范围谓词必须命中 nodes.id 唯一索引（替代已删的
// idx_nodes_func_id）。全表扫会在大仓上变成秒级/分钟级，故用 EXPLAIN QUERY
// PLAN 锁死计划形态。

import (
	"strings"
	"testing"
)

// TestFuncIDPrefixRangePlan：前缀范围谓词必须命中 nodes.id 唯一索引
// （替代已删的 idx_nodes_func_id；全表扫会在大仓上变成秒级/分钟级）。
func TestFuncIDPrefixRangePlan(t *testing.T) {
	r := newTestRepo(t)
	queries := map[string]struct {
		sql  string
		args []any
	}{
		"ssa_value 起点（trace 正向锚点）": {
			sql: `SELECT n.id FROM nodes n WHERE n.kind IN ('ssa_value','parameter')
				AND n.id >= ? AND n.id < ?
				AND (json_extract(n.properties, '$.type_string') = 'T'
				     OR json_extract(n.properties, '$.type_string') = '*T')`,
			args: []any{"symbol:go:m:f#", "symbol:go:m:f$"},
		},
		"函数内字段流（flows 锚点）": {
			sql: `SELECT n.id FROM nodes n WHERE n.kind = 'field_access'
				AND n.id >= ? AND n.id < ?`,
			args: []any{"symbol:go:m:f#", "symbol:go:m:f$"},
		},
		"同函数字段读取（rg_sql 桥）": {
			sql: `SELECT n2.id FROM nodes n1, nodes n2
				WHERE n1.id = 'symbol:go:m:f' AND n2.kind = 'field_access'
				  AND n2.id >= json_extract(n1.properties, '$.func_id') || '#'
				  AND n2.id < json_extract(n1.properties, '$.func_id') || '$'`,
			args: nil,
		},
	}
	for name, q := range queries {
		rows, err := r.Query("EXPLAIN QUERY PLAN "+q.sql, q.args...)
		if err != nil {
			t.Fatalf("%s explain: %v", name, err)
		}
		var plan strings.Builder
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				rows.Close()
				t.Fatalf("%s scan plan: %v", name, err)
			}
			plan.WriteString(detail + "\n")
		}
		rows.Close()
		if !strings.Contains(plan.String(), "sqlite_autoindex_nodes_1") {
			t.Errorf("%s 未命中 nodes.id 唯一索引：\n%s", name, plan.String())
		}
		if strings.Contains(plan.String(), "SCAN nodes") || strings.Contains(plan.String(), "SCAN n ") {
			t.Errorf("%s 退化为全表扫：\n%s", name, plan.String())
		}
	}
	// 真实 S3 查询（traceForwardSQL 原样）也必须走 id 索引锚点
	lo, hi := funcIDPrefixBounds("symbol:go:m:f")
	args := []any{"T.A", lo, hi, lo, hi, "T", "*T", "T", "*T",
		lo, hi, "T", "*T", "T.A", "T.A", "%.A", "%m.%", 8}
	rows, err := r.Query("EXPLAIN QUERY PLAN "+traceForwardSQL, args...)
	if err != nil {
		t.Fatalf("traceForwardSQL explain: %v", err)
	}
	var plan strings.Builder
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			rows.Close()
			t.Fatalf("scan plan: %v", err)
		}
		plan.WriteString(detail + "\n")
	}
	rows.Close()
	if !strings.Contains(plan.String(), "sqlite_autoindex_nodes_1") {
		t.Errorf("traceForwardSQL 锚点未命中 nodes.id 索引：\n%s", plan.String())
	}
	// 索引确实不再存在（新库不建、Open 幂等 DROP）
	if indexNames(t, r, "nodes")["idx_nodes_func_id"] {
		t.Error("idx_nodes_func_id 不应存在（P1-11 已删）")
	}
}
