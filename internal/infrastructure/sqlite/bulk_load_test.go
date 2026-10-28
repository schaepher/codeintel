package sqlite

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

func indexNames(t *testing.T, r *Repo, table string) map[string]bool {
	t.Helper()
	rows, err := r.Query("PRAGMA index_list(" + table + ")")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var seq int
		var name, origin string
		var unique, partial int
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			t.Fatal(err)
		}
		out[name] = true
	}
	return out
}

// TestBulkLoadIndexes（todo11-1）：Begin 删除可延后索引、写路径仍可用、
// End 重建；重建后按 func_id / full_path 查询可走索引且结果正确。
func TestBulkLoadIndexes(t *testing.T) {
	r := newTestRepo(t)
	// P1-11：函数内查询改走 nodes.id 前缀范围扫，表达式索引已删
	if indexNames(t, r, "nodes")["idx_nodes_func_id"] {
		t.Fatal("P1-11：idx_nodes_func_id 不应再创建（Open 时幂等 DROP）")
	}
	if err := r.BeginBulkLoad(); err != nil {
		t.Fatal(err)
	}
	for _, idx := range bulkLoadIndexes {
		if indexNames(t, r, idx.table)[idx.name] {
			t.Fatalf("BeginBulkLoad 后索引仍存在: %s", idx.name)
		}
	}
	// 写路径（唯一键索引仍在）可正常插入/合并
	n := node("f", "function", "F", "a.go")
	n.Properties["func_id"] = "symbol:go:m:F"
	save(t, r, []*domain.CodeEntity{n}, nil)
	fa := node("f#x", "field_access", "x", "a.go")
	fa.Properties["full_path"] = "T.X"
	save(t, r, []*domain.CodeEntity{fa}, nil)
	// 重复 ID 触发 upsert（唯一索引仍在）
	save(t, r, []*domain.CodeEntity{n}, nil)

	if err := r.EndBulkLoad(); err != nil {
		t.Fatal(err)
	}
	for _, idx := range bulkLoadIndexes {
		if !indexNames(t, r, idx.table)[idx.name] {
			t.Fatalf("EndBulkLoad 后索引缺失: %s", idx.name)
		}
	}
	var cnt int
	if err := r.QueryRow(`SELECT COUNT(*) FROM nodes WHERE json_extract(properties,'$.func_id')=?`, "symbol:go:m:F").Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 1 {
		t.Fatalf("func_id 查询 count=%d, want 1", cnt)
	}
	if err := r.QueryRow(`SELECT COUNT(*) FROM nodes WHERE kind='field_access' AND json_extract(properties,'$.full_path')=?`, "T.X").Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 1 {
		t.Fatalf("full_path 查询 count=%d, want 1", cnt)
	}
}
