package sqlite

// R85 物理物化：--base 分层——把 base 目录的 .codeintel/codeintel.db
// 索引数据一次性物化到本地（INSERT SELECT 秒级复制，非分析），之后
// 按包增量只写变更包。本地即完整索引，查询零改动；base 更新（commit
// 变化）自动重新物化（清空 + 复制 + 重新增量）。物化来源记录在
// build_metadata（tool_name='materialize'，commit_sha=base commit）——
// 下次对比同 base commit 时跳过。

import (
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"
)

// MaterializeBase 把 base 目录索引物化到本地（幂等：本地已物化同一
// base commit 时跳过）。返回是否执行了物化。
// 物化 = 清空本地图数据 + 复制 base 的 nodes/edges/摘要（FK 顺序）+
// 记录 materialize build_metadata（commit_sha=base 构建 commit）。
func (r *Repo) MaterializeBase(basePath string) (bool, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).MaterializeBase", zap.String("base", basePath))
	defer logger.Debug("exit (Repo).MaterializeBase")
	baseDB := filepath.Join(basePath, ".codeintel", "codeintel.db")
	if _, err := os.Stat(baseDB); err != nil {
		return false, fmt.Errorf("base 索引不存在: %w", err)
	}
	// ATTACH base（只读）→ base 构建 commit（物化对比键）
	if _, err := r.Exec(fmt.Sprintf("ATTACH DATABASE 'file:%s?mode=ro&immutable=1' AS base", baseDB)); err != nil {
		return false, fmt.Errorf("attach base: %w", err)
	}
	defer r.Exec("DETACH DATABASE base")
	var baseCommit string
	if err := r.QueryRow(`SELECT COALESCE(commit_sha, '') FROM base.build_metadata
		ORDER BY build_id DESC LIMIT 1`).Scan(&baseCommit); err != nil {
		return false, fmt.Errorf("读 base 构建记录: %w", err)
	}
	// 本地已物化同一 base commit → 跳过（本地 = base 数据 + 后续增量）
	var have string
	if err := r.QueryRow(`SELECT commit_sha FROM build_metadata
		WHERE tool_name = 'materialize' ORDER BY timestamp DESC LIMIT 1`).Scan(&have); err == nil && have == baseCommit {
		return false, nil
	}
	logger.Info("物化 base 索引", zap.String("base", basePath), zap.String("commit", baseCommit))
	if err := r.ResetGraphTables(); err != nil {
		return false, fmt.Errorf("reset local tables: %w", err)
	}
	tx, err := r.Begin()
	if err != nil {
		return false, fmt.Errorf("begin materialize tx: %w", err)
	}
	defer tx.Rollback()
	// FK 顺序：nodes → edges/摘要（子表引用父表）。nodes 的
	// signature_text 是生成列——显式列清单（不含生成列）
	cols := map[string]string{
		"nodes":                  "id, kind, name, file_path, line_start, line_end, properties",
		"edges":                  "id, source_id, target_id, kind, tool_source, confidence, metadata, count",
		"function_field_summary": "function_id, access_kind, field_path, instance_path, line_start, code_snippet",
		"summary_origins":        "function_id, access_kind, field_path, call_line, callee_id",
	}
	for _, tbl := range []string{"nodes", "edges", "function_field_summary", "summary_origins"} {
		if _, err := tx.Exec("INSERT INTO " + tbl + " (" + cols[tbl] + ") SELECT " + cols[tbl] + " FROM base." + tbl); err != nil {
			return false, fmt.Errorf("materialize %s: %w", tbl, err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO build_metadata (build_id, commit_sha, tool_name, status, timestamp)
		VALUES ('materialize', ?, 'materialize', 'success', strftime('%s', 'now'))
		ON CONFLICT(build_id) DO UPDATE SET commit_sha = excluded.commit_sha, timestamp = excluded.timestamp`,
		baseCommit); err != nil {
		return false, fmt.Errorf("record materialize: %w", err)
	}
	return true, tx.Commit()
}
