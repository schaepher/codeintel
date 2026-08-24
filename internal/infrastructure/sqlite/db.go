// Package sqlite 实现 Code Index Repository（TD.md 4.1/4.2）。
// SQLite 单文件存储图数据：nodes / edges / build_metadata。
// 说明：sqlite-vec 向量表（semble_vectors）在 Semble 适配器接入前不创建。
package sqlite

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
	"go.uber.org/zap"
)

// SchemaVersion 数据库 schema 版本（PRAGMA user_version）。
// v1.0 前无自动迁移：版本不匹配时提示手动重建（TD.md 10.2）。
// v2：新增 function_field_summary 表（SSA 字段追溯，field_trace.md §5.2）。
// v3：新增 summary_origins 表（Q161 间接写多来源聚合）。
// v4：新增 relation_candidates 表（P0③ 表关联缓存）+ 边复合索引（P0①）。
// Q235-3：user_version 仅作初次建库标记——v1→v4 演进全是加法（新表/
// 新索引），打开时总是幂等执行 schema DDL 自动补建（CREATE IF NOT
// EXISTS），无需 clean；列变更（幂等无法表达减法）由 verifySchema
// 报错提示 clean。不存 fingerprint（交替二进制无反复迁移：结构检查
// 是「期望列 ⊆ 实际列」子集判断，无写入状态）。
const SchemaVersion = 4

// Open 打开（或创建）仓库根目录下的 .codeintel/codeintel.db，并校验 schema 版本。
func Open(repoPath string) (*DB, error) {
	logger := zap.L()
	logger.Debug("enter Open")
	defer logger.Debug("exit Open")
	dir := filepath.Join(repoPath, ".codeintel")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create .codeintel dir: %w", err)
	}
	// 纯 Go 驱动（modernc.org/sqlite，driver 名 "sqlite"）：pragma 用
	// _pragma=name(value) 形式（busy_timeout 单写者 + WAL + 外键）。
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)",
		filepath.Join(dir, "codeintel.db"))
	raw, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// 单写者场景，限制连接池避免锁竞争
	raw.SetMaxOpenConns(1)

	db := &DB{DB: raw, repoPath: repoPath}
	if err := db.init(); err != nil {
		raw.Close()
		return nil, err
	}
	return db, nil
}

// DB 包装 *sql.DB，提供仓储实现。
type DB struct {
	*sql.DB
	repoPath string
}

func (db *DB) init() error {
	logger := zap.L()
	logger.Debug("enter (DB).init")
	defer logger.Debug("exit (DB).init")
	// Q235-3：user_version 仅作初次建库标记，不再严格相等校验——
	// schema 演进全为加法，打开时总是幂等执行补建缺失表/索引
	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	if v == 0 {
		if _, err := db.Exec(schema); err != nil {
			return fmt.Errorf("create schema: %w", err)
		}
		if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", SchemaVersion)); err != nil {
			return fmt.Errorf("set user_version: %w", err)
		}
	} else {
		// 旧库自动补建缺失表/索引（加法迁移）
		// R6：build_metadata 列加法幂等（SQLite 无 ADD COLUMN IF NOT
		// EXISTS——duplicate column 错误忽略）
		if _, err := db.Exec(`ALTER TABLE build_metadata ADD COLUMN degrade_stats TEXT`); err != nil &&
			!strings.Contains(err.Error(), "duplicate column") {
			return fmt.Errorf("add degrade_stats column: %w", err)
		}
		if _, err := db.Exec(schema); err != nil {
			// 缺列（破坏性变更）：补建表达式索引/生成列引用缺失列 →
			// 包装 clean 提示（verifySchema 覆盖不到的早期失败路径）
			if strings.Contains(err.Error(), "no such column") {
				return fmt.Errorf("schema mismatch: 列结构不兼容（%v）; run 'codeintel clean --repo <path> --force' and rebuild", err)
			}
			return fmt.Errorf("migrate schema: %w", err)
		}
	}
	// Q220c：配置表幂等补建（旧库自动获得 relation_rules，不要求 clean）
	if _, err := db.Exec(configSchema); err != nil {
		return fmt.Errorf("create config schema: %w", err)
	}
	// 结构齐全性检查：期望列 ⊆ 实际列（缺表已补建；缺列=破坏性变更
	// 幂等 DDL 无法表达 → 报错提示 clean）
	if err := db.verifySchema(); err != nil {
		return err
	}
	return nil
}

// schemaCols 各表期望列（Q235-3 结构齐全性检查）。schema 变更时须
// 同步更新本清单；列变更（含加列——CREATE IF NOT EXISTS 不动已存在
// 表）触发报错 clean，由清单与 DDL 不一致暴露。
var schemaCols = map[string][]string{
	"nodes":                 {"id", "kind", "name", "file_path", "line_start", "line_end", "properties", "signature_text", "created_at"},
	"edges":                 {"id", "source_id", "target_id", "kind", "tool_source", "confidence", "metadata"},
	"build_metadata":        {"build_id", "commit_sha", "tool_name", "status", "duration_ms", "error_message", "nodes_count", "edges_count", "timestamp"},
	"function_field_summary": {"function_id", "access_kind", "field_path", "instance_path", "line_start", "code_snippet"},
	"summary_origins":       {"function_id", "access_kind", "field_path", "call_line", "callee_id"},
	"relation_candidates":   {"build_id", "from_table", "from_col", "to_table", "to_col", "hops", "type"},
	"relation_rules":        {"id", "from_table", "from_col", "to_table", "to_col", "created_at"},
	"relation_progress":     {"build_id", "status", "done_count", "total_count", "updated_at"},
	"qa_history":            {"id", "question", "answer", "context", "agent", "created_at"},
}

// verifySchema 结构齐全性检查：每个核心表期望列全部存在。旧二进制
// 打开新库（实际列更多）通过——子集判断，不存 fingerprint 无反复
// 迁移（Q235-3）。
func (db *DB) verifySchema() error {
	for tbl, want := range schemaCols {
		// table_xinfo（非 table_info）：table_info 不返回生成列
		// （signature_text VIRTUAL 只在 xinfo 里）
		rows, err := db.Query("PRAGMA table_xinfo(" + tbl + ")")
		if err != nil {
			return fmt.Errorf("verify schema %s: %w", tbl, err)
		}
		have := map[string]bool{}
		for rows.Next() {
			var cid, notnull, pk, hidden int
			var name, typ string
			var dflt sql.NullString
			if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk, &hidden); err != nil {
				rows.Close()
				return fmt.Errorf("verify schema %s: %w", tbl, err)
			}
			have[name] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("verify schema %s: %w", tbl, err)
		}
		for _, c := range want {
			if !have[c] {
				return fmt.Errorf("schema mismatch: %s 缺列 %q（列变更无法自动迁移）; run 'codeintel clean --repo <path> --force' and rebuild", tbl, c)
			}
		}
	}
	return nil
}

// RepoPath 返回数据库所属仓库路径。
func (db *DB) RepoPath() string {
	logger := zap.L()
	logger.Debug("enter (DB).RepoPath")
	defer logger.Debug("exit (DB).RepoPath")
	return db.repoPath
}
