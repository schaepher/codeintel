package sqlite

// 旧库 schema 不兼容时的安全重建（reindex 路径）：在事务中重置**派生
// 图表**并重跑 schema DDL，保留人工数据（relation_rules / qa_history /
// relation_progress）与构建记录 build_metadata。失败回滚——数据库保持
// 原样，不删除文件（区别于 clean 的整目录删除）。

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
)

// rebuildDDL 重建用 DDL（测试可替换以验证「重置失败回滚」）。
var rebuildDDL = schema

// derivedDropList 重建时 DROP 的派生图表（视图先于表，子表先于父表）。
var derivedDropList = []struct{ kind, name string }{
	{"VIEW", "edges_v"},
	{"TABLE", "edges"},
	{"TABLE", "function_field_summary"},
	{"TABLE", "summary_origins"},
	{"TABLE", "relation_candidates"},
	{"TABLE", "nodes"},
}

// RebuildDerivedSchema 在旧库 schema 不兼容时安全重建派生图表：
//   - DROP 图数据表（nodes/edges/... 及兼容视图 edges_v）后重跑 schema；
//   - 保留 relation_rules / relation_progress / qa_history（人工数据）与
//     build_metadata（构建记录）；
//   - 全程单事务：任一步失败回滚，数据库保持原样（不删文件）。
func RebuildDerivedSchema(repoPath string) error {
	dir := filepath.Join(repoPath, ".codeintel")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create .codeintel dir: %w", err)
	}
	raw, err := sql.Open("sqlite", dbDSN(dir))
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}
	defer raw.Close()
	raw.SetMaxOpenConns(1)
	tx, err := raw.Begin()
	if err != nil {
		return fmt.Errorf("begin rebuild tx: %w", err)
	}
	defer tx.Rollback()
	for _, d := range derivedDropList {
		if _, err := tx.Exec("DROP " + d.kind + " IF EXISTS " + d.name); err != nil {
			return fmt.Errorf("drop %s %s: %w", d.kind, d.name, err)
		}
	}
	if _, err := tx.Exec(rebuildDDL); err != nil {
		return fmt.Errorf("rebuild schema: %w", err)
	}
	if _, err := tx.Exec(configSchema); err != nil {
		return fmt.Errorf("ensure config schema: %w", err)
	}
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", SchemaVersion)); err != nil {
		return fmt.Errorf("set user_version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit rebuild: %w", err)
	}
	return nil
}
