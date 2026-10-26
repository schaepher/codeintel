package sqlite

// 冗余索引清理（Q254 Stage 1 / P1-11）：低价值或"最左前缀重复"的索引在
// Open 时幂等 DROP——删索引不改表结构，不递增 SchemaVersion，不需要重建库。

import "fmt"

// redundantEdgeIndexes Q254：低价值/冗余的 edges 索引（DROP INDEX 即可，
// 不影响表结构 → 不需要重建库，故不递增 SchemaVersion）。
var redundantEdgeIndexes = []string{
	"idx_edges_source",      // UNIQUE(source_id,target_id,kind) 最左前缀 + 覆盖
	"idx_edges_source_kind", // 同上（kind 是 UNIQUE 索引列）
	"idx_edges_target",      // idx_edges_target_kind(target_id,kind) 最左前缀顶替
	"idx_edges_confidence",  // 无查询按 confidence 过滤（仅 SELECT 列）
}

// redundantSummaryIndexes P1-11：摘要表的**前缀冗余**索引——与 edges 同理
// （DROP INDEX 不改表结构，幂等执行，不递增 SchemaVersion）。
//
//   - idx_summary_func_access(function_id, access_kind) 是 UNIQUE(function_id,
//     access_kind, field_path) 的最左前缀（go2o：2.5MB / 114MB）
//   - idx_summary_origins_func(function_id, access_kind) 是 UNIQUE(function_id,
//     access_kind, field_path, call_line, callee_id) 的最左前缀（0.4MB）
//
// 查询计划由 dbdiag --plans 复核（SEARCH ... USING COVERING INDEX
// sqlite_autoindex_function_field_summary_1）。
var redundantSummaryIndexes = []string{
	"idx_summary_func_access",
	"idx_summary_origins_func",
}

// dropRedundantEdgeIndexes 删除冗余 edges 索引；返回实际删除的名字（幂等）。
func dropRedundantEdgeIndexes(db *DB) ([]string, error) {
	return dropIndexList(db, redundantEdgeIndexes)
}

// dropRedundantSummaryIndexes 删除摘要表的冗余前缀索引；返回实删名字（幂等）。
func dropRedundantSummaryIndexes(db *DB) ([]string, error) {
	return dropIndexList(db, redundantSummaryIndexes)
}

// dropIndexList 幂等删除一组索引（不存在则跳过）。
func dropIndexList(db *DB, indexes []string) ([]string, error) {
	var dropped []string
	for _, idx := range indexes {
		var n int
		if err := db.QueryRow(
			"SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?", idx).Scan(&n); err != nil {
			return dropped, fmt.Errorf("check index %s: %w", idx, err)
		}
		if n == 0 {
			continue
		}
		if _, err := db.Exec("DROP INDEX " + idx); err != nil {
			return dropped, fmt.Errorf("drop index %s: %w", idx, err)
		}
		dropped = append(dropped, idx)
	}
	return dropped, nil
}
