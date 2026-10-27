package sqlite

// P1-03：把 cli 里残留的裸 SQL 收口到仓储层——"最新构建的新鲜度"
// （stale 判定用）与"最新 commit_sha"（增量变更检测用）。两者口径必须
// 与 Repo.GetLatest 一致：timestamp DESC, rowid DESC（同一秒多次构建按
// 写入顺序取最新）。

import (
	"database/sql"
	"errors"
	"path/filepath"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// LatestBuildFreshness 最新构建的 (commit_sha, timestamp, worktree 指纹)。
// 无构建记录 → domain.ErrNotFound。
func (r *Repo) LatestBuildFreshness() (sha string, timestamp int64, fingerprint string, err error) {
	logger := zap.L()
	logger.Debug("enter (Repo).LatestBuildFreshness")
	defer logger.Debug("exit (Repo).LatestBuildFreshness")
	err = r.QueryRow(`SELECT COALESCE(commit_sha,''), timestamp,
		COALESCE(worktree_fingerprint,'')
		FROM build_metadata ORDER BY timestamp DESC, rowid DESC LIMIT 1`).
		Scan(&sha, &timestamp, &fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, "", domain.ErrNotFound
	}
	return sha, timestamp, fingerprint, err
}

// LatestCommitSHAFromPath 只读打开 <repoPath>/.codeintel/codeintel.db 取最新
// commit_sha（增量变更检测在"尚未建索引/旧 schema"时也要能问）。
//
// 与 Open 的区别：不建目录、不做 schema 迁移/校验——库不存在或读取失败
// 一律返回空串（调用方回退工作区检测）。
func LatestCommitSHAFromPath(repoPath string) string {
	logger := zap.L()
	logger.Debug("enter LatestCommitSHAFromPath")
	defer logger.Debug("exit LatestCommitSHAFromPath")
	path := filepath.Join(repoPath, ".codeintel", "codeintel.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return ""
	}
	defer db.Close()
	var sha string
	if err := db.QueryRow(`SELECT COALESCE(commit_sha,'') FROM build_metadata
		ORDER BY timestamp DESC, rowid DESC LIMIT 1`).Scan(&sha); err != nil {
		return ""
	}
	return sha
}
