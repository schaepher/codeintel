package sqlite

// 冗余索引清理的测试（Q254 Stage 1 的 edges 索引 + P1-11 的摘要表前缀索引）。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

func TestDropRedundantEdgeIndexes(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	repo := NewRepo(db)
	save(t, repo, []*domain.CodeEntity{
		{ID: "symbol:go:x:a", Kind: domain.KindFunction, Name: "a"},
		{ID: "symbol:go:x:b", Kind: domain.KindFunction, Name: "b"},
	}, []*domain.Fact{{SourceID: "symbol:go:x:a", TargetID: "symbol:go:x:b", Kind: domain.FactCalls}})

	// 新库不该有冗余索引
	for _, idx := range redundantEdgeIndexes {
		if hasIndex(t, db, idx) {
			t.Errorf("新建库不应存在 %s（schema.go 已移除）", idx)
		}
	}
	// 必需的索引仍在
	for _, idx := range []string{"idx_edges_kind", "idx_edges_target_kind"} {
		if !hasIndex(t, db, idx) {
			t.Errorf("必需索引 %s 缺失", idx)
		}
	}

	// 模拟旧库：手工建回冗余索引 → 再跑迁移
	for _, idx := range redundantEdgeIndexes {
		stmt := "CREATE INDEX " + idx + " ON edges(source_ref)"
		switch idx {
		case "idx_edges_source_kind":
			stmt = "CREATE INDEX " + idx + " ON edges(source_ref, kind)"
		case "idx_edges_target":
			stmt = "CREATE INDEX " + idx + " ON edges(target_ref)"
		case "idx_edges_confidence":
			stmt = "CREATE INDEX " + idx + " ON edges(confidence) WHERE confidence >= 0.8"
		}
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("建旧索引 %s: %v", idx, err)
		}
	}
	dropped, err := dropRedundantEdgeIndexes(db)
	if err != nil {
		t.Fatalf("dropRedundantEdgeIndexes: %v", err)
	}
	if len(dropped) != len(redundantEdgeIndexes) {
		t.Fatalf("应删 %d 个，实删 %v", len(redundantEdgeIndexes), dropped)
	}
	for _, idx := range redundantEdgeIndexes {
		if hasIndex(t, db, idx) {
			t.Errorf("%s 未被删除", idx)
		}
	}
	// 幂等：再跑一次无动作无错误
	again, err := dropRedundantEdgeIndexes(db)
	if err != nil || len(again) != 0 {
		t.Errorf("迁移应幂等：dropped=%v err=%v", again, err)
	}
	// 数据未受影响
	nodes, edges, err := repo.Counts()
	if err != nil || nodes != 2 || edges != 1 {
		t.Errorf("迁移后数据异常：nodes=%d edges=%d err=%v", nodes, edges, err)
	}
}

// Q254：关闭外键构建时插入的悬挂边，末尾一次性清理并按条数报告。
// P1-11：摘要表前缀冗余索引在 Open 时幂等删除；旧库（手工建回）也能删掉，
// 且唯一索引仍在（查询仍走 UNIQUE 前缀）。
func TestDropRedundantSummaryIndexes(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	r := NewRepo(db)
	for _, idx := range redundantSummaryIndexes {
		if hasIndex(t, db, idx) {
			t.Fatalf("新库不应再建 %s", idx)
		}
	}
	for _, idx := range []string{"sqlite_autoindex_function_field_summary_1", "sqlite_autoindex_summary_origins_1"} {
		if !hasIndex(t, db, idx) {
			t.Errorf("UNIQUE 索引 %s 应存在", idx)
		}
	}
	// 模拟旧库：建回冗余索引 → 幂等删除
	if _, err := db.Exec(`CREATE INDEX idx_summary_func_access ON function_field_summary(function_id, access_kind)`); err != nil {
		t.Fatalf("建旧索引: %v", err)
	}
	if _, err := db.Exec(`CREATE INDEX idx_summary_origins_func ON summary_origins(function_id, access_kind)`); err != nil {
		t.Fatalf("建旧索引: %v", err)
	}
	dropped, err := dropRedundantSummaryIndexes(db)
	if err != nil {
		t.Fatalf("dropRedundantSummaryIndexes: %v", err)
	}
	if len(dropped) != len(redundantSummaryIndexes) {
		t.Fatalf("应删 %d 个，实删 %v", len(redundantSummaryIndexes), dropped)
	}
	again, err := dropRedundantSummaryIndexes(db)
	if err != nil || len(again) != 0 {
		t.Errorf("迁移应幂等：dropped=%v err=%v", again, err)
	}
	// 查询计划仍走 UNIQUE 索引（不因删索引退化为全表扫）
	rows, err := r.Query(`EXPLAIN QUERY PLAN SELECT function_id, access_kind, field_path
		FROM function_field_summary WHERE function_id = ? ORDER BY access_kind, field_path`, "symbol:go:m:F")
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()
	found := ""
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan: %v", err)
		}
		found += detail + "\n"
	}
	if !strings.Contains(found, "sqlite_autoindex_function_field_summary_1") {
		t.Fatalf("摘要查询未走 UNIQUE 索引：%s", found)
	}
}
