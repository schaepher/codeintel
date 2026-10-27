package sqlite

// todo11-1：全量构建期延后非关键索引（bulk load）。
//
// 构建写路径（SaveBatchStats）只需要：nodes.id 唯一索引（端点解析）、
// edges UNIQUE(source_ref,target_ref,kind)（同义边合并）、摘要唯一键。
// 其余查询索引（按 kind/name/file/JSON 表达式）在 118k+136k 行逐条插入
// 时持续维护，SQLite 实测占 flush CPU 的大头（json_extract 索引尤其
// 昂贵）。全量构建先把它们 DROP，写完后一次 CREATE INDEX（排序建树，
// 远比随机插入维护便宜）。
//
// 安全性：索引缺失期间只有构建写路径在访问库；构建失败时 EndBulkLoad
// 仍由 defer 执行；进程被杀则下次 Open 的幂等 schema 会重建索引。

import "fmt"

// bulkLoadIndexes 可延后的查询索引（顺序无关；定义与 schema.go 一致）。
var bulkLoadIndexes = []struct {
	table string
	name  string
	ddl   string
}{
	{"nodes", "idx_nodes_file_kind", "CREATE INDEX IF NOT EXISTS idx_nodes_file_kind ON nodes(file_path, kind) WHERE file_path IS NOT NULL"},
	{"nodes", "idx_nodes_name", "CREATE INDEX IF NOT EXISTS idx_nodes_name ON nodes(name)"},
	{"nodes", "idx_nodes_field_path", "CREATE INDEX IF NOT EXISTS idx_nodes_field_path ON nodes(json_extract(properties, '$.full_path')) WHERE kind = 'field_access'"},
	{"nodes", "idx_nodes_func_id", "CREATE INDEX IF NOT EXISTS idx_nodes_func_id ON nodes(json_extract(properties, '$.func_id'))"},
	{"nodes", "idx_nodes_grpc_service", "CREATE INDEX IF NOT EXISTS idx_nodes_grpc_service ON nodes(kind) WHERE kind = 'grpc_service'"},
	{"nodes", "idx_nodes_registers_service", "CREATE INDEX IF NOT EXISTS idx_nodes_registers_service ON nodes(json_extract(properties, '$.registers_service'))"},
	{"edges", "idx_edges_kind", "CREATE INDEX IF NOT EXISTS idx_edges_kind ON edges(kind)"},
	{"edges", "idx_edges_target_kind", "CREATE INDEX IF NOT EXISTS idx_edges_target_kind ON edges(target_ref, kind)"},
	{"function_field_summary", "idx_summary_field", "CREATE INDEX IF NOT EXISTS idx_summary_field ON function_field_summary(field_path)"},
}

// BeginBulkLoad 全量构建前调用：删除可延后的查询索引。
func (r *Repo) BeginBulkLoad() error {
	for _, idx := range bulkLoadIndexes {
		if _, err := r.Exec("DROP INDEX IF EXISTS " + idx.name); err != nil {
			return fmt.Errorf("drop index %s: %w", idx.name, err)
		}
	}
	return nil
}

// EndBulkLoad 全量构建写完后调用：重建查询索引（排序建树）。
func (r *Repo) EndBulkLoad() error {
	for _, idx := range bulkLoadIndexes {
		if _, err := r.Exec(idx.ddl); err != nil {
			return fmt.Errorf("create index %s: %w", idx.name, err)
		}
	}
	return nil
}
