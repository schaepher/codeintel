package sqlite

// #237 recent_changes：最近变更（Agent 接手仓库先看动态）——commit 节点
// 按日期降序 → MODIFIED_BY 变更文件 → 文件内顶层符号（≤5）。

import (
	"encoding/json"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// RecentChanges 最近变更（#237）：commit 节点（kind=commit）按 date 降序
// 取 limit 条，每条聚合 MODIFIED_BY 文件（file 节点 → commit 边）与文件
// 内顶层符号（function/method/struct/interface，排除字段追溯内部节点）。
func (r *Repo) RecentChanges(limit int) ([]*domain.RecentChange, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).RecentChanges", zap.Int("limit", limit))
	defer logger.Debug("exit (Repo).RecentChanges")
	if limit <= 0 {
		limit = 10
	}
	rows, err := r.Query(
		`SELECT id, name, properties FROM nodes WHERE kind = 'commit'
		 ORDER BY json_extract(properties, '$.date') DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	// 先收完外层行再聚合（单连接上外层 rows 迭代中开新 Query 会挂起
	// 死锁——AGENTS.md 断言坑）
	var raw []*domain.RecentChange
	for rows.Next() {
		var id, name, props string
		if err := rows.Scan(&id, &name, &props); err != nil {
			rows.Close()
			return nil, err
		}
		rc := &domain.RecentChange{
			CommitSHA: id, Name: name,
			ShortSHA: shortSHAFromID(id),
		}
		if m := propsMap(props); m != nil {
			rc.Date, _ = m["date"].(string)
			rc.Message, _ = m["message"].(string)
		}
		raw = append(raw, rc)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// 逐 commit 聚合变更文件（外层已关闭）
	for _, rc := range raw {
		files, err := r.changedFiles(rc.CommitSHA)
		if err != nil {
			return nil, err
		}
		rc.Files = files
	}
	return raw, nil
}

// changedFiles 某 commit 的 MODIFIED_BY 文件 + 文件内顶层符号。
func (r *Repo) changedFiles(commitID string) ([]domain.ChangeFile, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).changedFiles", zap.String("commit", commitID))
	defer logger.Debug("exit (Repo).changedFiles")
	rows, err := r.Query(
		"SELECT source_id FROM edges WHERE kind = 'modified_by' AND target_id = ? ORDER BY source_id", commitID)
	if err != nil {
		return nil, err
	}
	// 先收完文件行再查符号（单连接死锁防护，同上）
	var paths []string
	for rows.Next() {
		var src string
		if err := rows.Scan(&src); err != nil {
			rows.Close()
			return nil, err
		}
		paths = append(paths, strings.TrimPrefix(src, "file:"))
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	files := make([]domain.ChangeFile, 0, len(paths))
	for _, path := range paths {
		syms, err := r.topSymbolsInFile(path)
		if err != nil {
			return nil, err
		}
		files = append(files, domain.ChangeFile{Path: path, Symbols: syms})
	}
	return files, nil
}

// topSymbolsInFile 文件内顶层符号（≤5；排除字段追溯内部节点）。
func (r *Repo) topSymbolsInFile(file string) ([]domain.SymbolBrief, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).topSymbolsInFile", zap.String("file", file))
	defer logger.Debug("exit (Repo).topSymbolsInFile")
	const exclude = "kind NOT IN ('field_access','ssa_value','external_summary','parameter','receiver','result')"
	rows, err := r.Query(
		`SELECT name, kind, line_start FROM nodes WHERE file_path = ? AND `+exclude+`
		 ORDER BY line_start LIMIT 5`, file)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.SymbolBrief
	for rows.Next() {
		var name, kind string
		var line int
		if err := rows.Scan(&name, &kind, &line); err != nil {
			return nil, err
		}
		out = append(out, domain.SymbolBrief{Name: name, Kind: kind, File: file, Line: line})
	}
	return out, rows.Err()
}

// shortSHAFromID commit:<sha> → SHA 前 12 位（与 git 适配器 shortSHA 一致）。
func shortSHAFromID(id string) string {
	s := strings.TrimPrefix(id, "commit:")
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// propsMap 解析 properties JSON（nil 容错）。
func propsMap(s string) map[string]any {
	if s == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil
	}
	return m
}
