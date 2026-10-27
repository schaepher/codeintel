package sqlite

// P1-03：cli 里的裸 SQL 收口——"最新构建的新鲜度"（stale 判定）与
// "最新 commit_sha"（增量变更检测）改由仓储提供，cli 不再自带 SQL。

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestLatestBuildFreshness 最新构建三元组（commit_sha/timestamp/指纹）：
// 取最后一条（timestamp 相同按 rowid）；无构建记录 → ErrNotFound。
func TestLatestBuildFreshness(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	r := NewRepo(db)

	if _, _, _, err := r.LatestBuildFreshness(); err == nil {
		t.Error("无构建记录应返回 error")
	}

	for _, m := range []*domain.BuildMeta{
		{BuildID: "b1", CommitSHA: "sha1", ToolName: "all", Status: domain.BuildSuccess},
		{BuildID: "b2", CommitSHA: "sha2", ToolName: "all", Status: domain.BuildSuccess,
			WorktreeFingerprint: `{"a.go":"deadbeef"}`},
	} {
		if err := r.Save(m); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	sha, ts, fp, err := r.LatestBuildFreshness()
	if err != nil {
		t.Fatalf("LatestBuildFreshness: %v", err)
	}
	if sha != "sha2" || fp != `{"a.go":"deadbeef"}` || ts <= 0 {
		t.Errorf("freshness = (%q, %d, %q)", sha, ts, fp)
	}
}

// TestLatestCommitSHAFromPath 只读路径查询（索引库不存在/无记录 → 空串，
// 且不得创建 .codeintel 目录）。
func TestLatestCommitSHAFromPath(t *testing.T) {
	dir := t.TempDir()
	if got := LatestCommitSHAFromPath(dir); got != "" {
		t.Errorf("未建索引时 = %q, want 空串", got)
	}

	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := NewRepo(db).Save(&domain.BuildMeta{BuildID: "b1", CommitSHA: "abc123",
		ToolName: "all", Status: domain.BuildSuccess}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	db.Close()

	if got := LatestCommitSHAFromPath(dir); got != "abc123" {
		t.Errorf("LatestCommitSHAFromPath = %q, want abc123", got)
	}
}
