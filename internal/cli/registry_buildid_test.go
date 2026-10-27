package cli

// todo23 §10.1：构建结果 / build_metadata / 全局台账共用实际 BuildID；
// HeadCommit 单独保存（不能用 commit 充当 BuildID）。

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

func buildMetaOf(t *testing.T, dir string) string {
	t.Helper()
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	meta, err := sqlite.NewRepo(db).GetLatest()
	if err != nil || meta == nil {
		t.Fatalf("build_metadata 缺失: %v", err)
	}
	return meta.BuildID
}

// TestBuildIDMatchesMetadataAndRegistry：同 HEAD 连续 init 两次 → 台账
// BuildID 与 build_metadata 相等且非空；两次 BuildID 不同、HeadCommit 相同；
// RegisteredAt 保留。
func TestBuildIDMatchesMetadataAndRegistry(t *testing.T) {
	isolateRegistryDir(t)
	dir := seedGitRepo(t)

	if code := cmdInit(context.Background(), []string{"--repo", dir}); code != 0 {
		t.Fatalf("第一次 init exit = %d", code)
	}
	meta1 := buildMetaOf(t, dir)
	reg1 := registryFind(t, dir)
	if meta1 == "" || reg1.BuildID == "" || reg1.BuildID != meta1 {
		t.Fatalf("台账 BuildID 必须等于 build_metadata：meta=%q reg=%q", meta1, reg1.BuildID)
	}
	if reg1.HeadCommit == "" || reg1.HeadCommit == reg1.BuildID {
		t.Fatalf("HeadCommit 必须单独保存：head=%q build=%q", reg1.HeadCommit, reg1.BuildID)
	}

	if code := cmdInit(context.Background(), []string{"--repo", dir}); code != 0 {
		t.Fatalf("第二次 init exit = %d", code)
	}
	meta2 := buildMetaOf(t, dir)
	reg2 := registryFind(t, dir)
	if reg2.BuildID == "" || reg2.BuildID != meta2 {
		t.Fatalf("第二次台账 BuildID 必须等于 build_metadata：meta=%q reg=%q", meta2, reg2.BuildID)
	}
	if reg1.BuildID == reg2.BuildID {
		t.Errorf("同 HEAD 两次构建的 BuildID 应不同：%q", reg1.BuildID)
	}
	if reg1.HeadCommit != reg2.HeadCommit {
		t.Errorf("HeadCommit 应相同：%q vs %q", reg1.HeadCommit, reg2.HeadCommit)
	}
	if reg1.RegisteredAt == "" || reg2.RegisteredAt != reg1.RegisteredAt {
		t.Errorf("重复 init 应保留 RegisteredAt：%q vs %q", reg1.RegisteredAt, reg2.RegisteredAt)
	}
}

// TestBuildIDWithoutGit：无自身 .git 的独立模块（即使位于父仓库目录下）
// BuildID 有效、HeadCommit 为空，不继承父 HEAD。
func TestBuildIDWithoutGit(t *testing.T) {
	isolateRegistryDir(t)
	parent := t.TempDir()
	if err := os.MkdirAll(filepath.Join(parent, "mod"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(parent, "mod")
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package m\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := cmdInit(context.Background(), []string{"--repo", dir}); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	meta := buildMetaOf(t, dir)
	reg := registryFind(t, dir)
	if meta == "" || reg.BuildID != meta {
		t.Errorf("无 git 模块 BuildID 仍应有效：meta=%q reg=%q", meta, reg.BuildID)
	}
	if reg.HeadCommit != "" {
		t.Errorf("无自身 .git 不得继承父 HEAD：%q", reg.HeadCommit)
	}
}
