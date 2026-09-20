package sqlite

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// Q246 schema 优化回归：① 无用索引 idx_nodes_signature 不再创建（旧库
// 打开时 DROP）；② full_path 表达式索引改部分索引（带 kind='field_access'
// 谓词的查询仍走索引——SQLite 用部分索引要求谓词在 WHERE 顶层合取式中
// 出现）；③ edges.id 去 AUTOINCREMENT。

// TestSchemaNewDBShape 新库结构：三个改动都体现在 sqlite_master 里。
func TestSchemaNewDBShape(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	// sqlite_master 原样存 SQL 文本（含注释）——比对前去掉 -- 注释行
	ddl := func(name string) string {
		t.Helper()
		var sql string
		if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = ?`, name).Scan(&sql); err != nil {
			t.Fatalf("sqlite_master %s: %v", name, err)
		}
		var keep []string
		for _, line := range strings.Split(sql, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "--") {
				continue
			}
			keep = append(keep, line)
		}
		return strings.Join(keep, "\n")
	}

	if s := ddl("edges"); strings.Contains(strings.ToUpper(s), "AUTOINCREMENT") {
		t.Errorf("edges.id 不应再带 AUTOINCREMENT：%s", s)
	}
	if s := ddl("idx_nodes_field_path"); !strings.Contains(s, "WHERE kind = 'field_access'") {
		t.Errorf("idx_nodes_field_path 应为部分索引（kind='field_access'），got: %s", s)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_nodes_signature'`).Scan(&n); err != nil {
		t.Fatalf("count idx_nodes_signature: %v", err)
	}
	if n != 0 {
		t.Errorf("新库不应创建 idx_nodes_signature")
	}
}

// TestSchemaLegacyUnusedIndexDropped：旧库（v1Schema 建了
// idx_nodes_signature）打开时自动 DROP——CREATE INDEX 无法表达减法。
func TestSchemaLegacyUnusedIndexDropped(t *testing.T) {
	dir := t.TempDir()
	raw := openRaw(t, dir, v1Schema, 1)
	// 先确认旧库确实有该索引（否则本用例测的是空气）
	var n int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_nodes_signature'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("v1 旧库应含 idx_nodes_signature，got %d", n)
	}
	raw.Close()

	// Q254c：v1 旧库（TEXT 端点）现在要求重建——本用例改为验证"新库里
	// 不再创建 idx_nodes_signature"（旧库清理路径由 schema_migrate_test 覆盖）
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open 新库: %v", err)
	}
	defer db.Close()
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_nodes_signature'`).Scan(&n); err != nil {
		t.Fatalf("count after open: %v", err)
	}
	if n != 0 {
		t.Errorf("旧库打开后应 DROP idx_nodes_signature，仍存在 %d 个", n)
	}
}

// TestFieldPathIndexUsedByRealQueries：真实查询形态仍走部分索引（沿用
// repo_trace.go 锚点 + repo_dispatch.go FindFieldReads 的 WHERE 形态）。
// 断言 EXPLAIN QUERY PLAN——避免「改了索引但查询悄悄退化成全表扫」。
func TestFieldPathIndexUsedByRealQueries(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	r := NewRepo(db)
	nodes := []*domain.CodeEntity{}
	for i := 0; i < 200; i++ {
		kind := domain.KindFieldAccess
		if i%2 == 1 {
			kind = domain.KindSSAValue
		}
		nodes = append(nodes, &domain.CodeEntity{
			ID:   domain.CanonicalID("symbol:go:mtest:f" + string(rune('a'+i%26)) + "#" + string(rune('0'+i%10)) + string(rune('A'+i/10))),
			Kind: kind, Name: "x", Properties: map[string]any{
				"full_path": "t.a", "func_id": "symbol:go:mtest:f",
			},
		})
	}
	if _, err := r.SaveBatchStats(nodes, nil, nil); err != nil {
		t.Fatalf("save nodes: %v", err)
	}

	queries := map[string]string{
		"trace 锚点（别名限定）": `SELECT id FROM nodes n WHERE n.kind = 'field_access'
			AND json_extract(n.properties, '$.full_path') = 't.a'`,
		"FindFieldReads（无别名）": `SELECT id, kind, name FROM nodes WHERE kind = 'field_access'
			AND json_extract(properties, '$.access_kind') = 'read'
			AND json_extract(properties, '$.full_path') = 't.a' ORDER BY line_start, id`,
	}
	for name, q := range queries {
		plan := queryPlan(t, db, q)
		if !strings.Contains(plan, "idx_nodes_field_path") {
			t.Errorf("%s: 查询未走部分索引，plan=%s", name, plan)
		}
	}
}

// queryPlan 返回 EXPLAIN QUERY PLAN 的合并文本。
func queryPlan(t *testing.T, db *DB, query string) string {
	t.Helper()
	rows, err := db.Query("EXPLAIN QUERY PLAN " + query)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		b.WriteString(detail)
		b.WriteString("; ")
	}
	return b.String()
}

// TestSchemaDQLEdgesNoAutoincrementForFile：语义完整性——edges 表仍可用
// （去 AUTOINCREMENT 后 rowid 自动分配、UPSERT 合并照常）。
func TestEdgesStillUpsertWithoutAutoincrement(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	r := NewRepo(db)
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:mtest:a", Kind: domain.KindFunction, Name: "a", FilePath: "a.go"},
		{ID: "symbol:go:mtest:b", Kind: domain.KindFunction, Name: "b", FilePath: "a.go"},
	}
	edges := []*domain.Fact{{SourceID: "symbol:go:mtest:a", TargetID: "symbol:go:mtest:b",
		Kind: domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.8}}
	if _, err := r.SaveBatchStats(nodes, edges, nil); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := r.SaveBatchStats(nil, edges, nil); err != nil {
		t.Fatalf("resave: %v", err)
	}
	var cnt, calls int
	if err := db.QueryRow(`SELECT COUNT(*), MAX(count) FROM edges`).Scan(&cnt, &calls); err != nil {
		t.Fatalf("count edges: %v", err)
	}
	if cnt != 1 || calls != 2 {
		t.Errorf("同义边应合并为 1 行且 count=2，got rows=%d count=%d", cnt, calls)
	}
}
