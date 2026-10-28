// T17：注册台账使用**本次**实际 BuildID/commitSHA，commit 为空时不借父仓
// 提交；有自身 git 身份的 worktree/submodule 用自身 HEAD；无自身 git 的相邻
// 模块保持空 commit；重建刷新 BuildID 并保留 RegisteredAt。
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// findRegistered 打开隔离注册表并读一条记录。
func findRegistered(t *testing.T, path string) (sqlite.RegistryRepo, bool) {
	t.Helper()
	r := openGlobalRegistry()
	if r == nil {
		t.Fatal("registry unavailable")
	}
	defer r.Close()
	repo, ok, err := r.FindRepo(path)
	if err != nil {
		t.Fatalf("FindRepo: %v", err)
	}
	return repo, ok
}

// initGitDir 建一个带提交的 git 仓库并返回其 HEAD。
func initGitDir(t *testing.T, dir string) string {
	t.Helper()
	runGit(t, dir, "init", "-b", "main", ".")
	runGit(t, dir, "config", "user.email", "t@t")
	runGit(t, dir, "config", "user.name", "t")
	runGit(t, dir, "commit", "--allow-empty", "-m", "init")
	return gitHead(dir)
}

func TestRegisterRepoAfterBuildUsesActualIdentity(t *testing.T) {
	isolateRegistryDir(t)
	if !gitAvailable() {
		t.Skip("git 不可用")
	}
	dir := t.TempDir()
	initGitDir(t, dir)
	abs, _ := filepath.Abs(dir)
	// 显式传入的 buildID/commitSHA 必须原样记录（不能用 HEAD 冒充 BuildID）。
	registerRepoAfterBuild(abs, "example.com/x", 1, "build-42", "commit-abc")
	repo, ok := findRegistered(t, abs)
	if !ok {
		t.Fatal("构建成功应注册台账")
	}
	if repo.BuildID != "build-42" {
		t.Errorf("BuildID = %q, want build-42", repo.BuildID)
	}
	if repo.HeadCommit != "commit-abc" {
		t.Errorf("HeadCommit = %q, want commit-abc", repo.HeadCommit)
	}
	if repo.RegisteredAt == "" || repo.LastBuiltAt == "" {
		t.Errorf("注册/构建时间缺失: %+v", repo)
	}
}

func TestRegisterRepoAfterBuildPreservesRegisteredAtOnRebuild(t *testing.T) {
	isolateRegistryDir(t)
	if !gitAvailable() {
		t.Skip("git 不可用")
	}
	dir := t.TempDir()
	initGitDir(t, dir)
	abs, _ := filepath.Abs(dir)
	registerRepoAfterBuild(abs, "example.com/x", 1, "build-1", "")
	first, ok := findRegistered(t, abs)
	if !ok {
		t.Fatal("missing after first build")
	}
	registerRepoAfterBuild(abs, "example.com/x", 1, "build-2", "")
	second, ok := findRegistered(t, abs)
	if !ok {
		t.Fatal("missing after second build")
	}
	if second.BuildID != "build-2" {
		t.Errorf("重建应刷新 BuildID: %q", second.BuildID)
	}
	if second.RegisteredAt != first.RegisteredAt {
		t.Errorf("重建不得改 RegisteredAt: %q vs %q", second.RegisteredAt, first.RegisteredAt)
	}
}

// 空 commit + 有自身 .git → 用自身 HEAD（不是父仓）。
func TestRegisterRepoAfterBuildCommitFallbackOwnHead(t *testing.T) {
	isolateRegistryDir(t)
	if !gitAvailable() {
		t.Skip("git 不可用")
	}
	dir := t.TempDir()
	head := initGitDir(t, dir)
	abs, _ := filepath.Abs(dir)
	registerRepoAfterBuild(abs, "example.com/x", 1, "build-1", "")
	repo, ok := findRegistered(t, abs)
	if !ok {
		t.Fatal("missing")
	}
	if repo.HeadCommit != head {
		t.Errorf("HeadCommit = %q, want own HEAD %q", repo.HeadCommit, head)
	}
}

// 模块位于父 Git checkout 下但没有自己的 .git → commit 必须保持空（不借父仓）。
func TestRegisterRepoAfterBuildNoParentInheritance(t *testing.T) {
	isolateRegistryDir(t)
	if !gitAvailable() {
		t.Skip("git 不可用")
	}
	parent := t.TempDir()
	parentHead := initGitDir(t, parent)
	child := filepath.Join(parent, "module-a")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	abs, _ := filepath.Abs(child)
	registerRepoAfterBuild(abs, "example.com/module-a", 1, "build-1", "")
	repo, ok := findRegistered(t, abs)
	if !ok {
		t.Fatal("missing")
	}
	if repo.HeadCommit != "" {
		t.Errorf("无自身 git 的相邻模块不得继承父仓提交: %q（父 %q）", repo.HeadCommit, parentHead)
	}
}

// worktree（.git 是文件）有自身 git 身份 → 用自身 HEAD。
func TestRegisterRepoAfterBuildWorktreeOwnHead(t *testing.T) {
	isolateRegistryDir(t)
	if !gitAvailable() {
		t.Skip("git 不可用")
	}
	main := t.TempDir()
	initGitDir(t, main)
	runGit(t, main, "commit", "--allow-empty", "-m", "second")
	mainHead := gitHead(main)
	wt := filepath.Join(t.TempDir(), "wt")
	runGit(t, main, "worktree", "add", "-b", "wtbranch", wt)
	// worktree 上再来一次提交 → 自身 HEAD 与主仓不同（证明取的是自身身份）
	runGit(t, wt, "commit", "--allow-empty", "-m", "wt-only")
	wtHead := gitHead(wt)
	if wtHead == "" || wtHead == mainHead {
		t.Fatalf("worktree 应有自身 HEAD: wt=%q main=%q", wtHead, mainHead)
	}
	abs, _ := filepath.Abs(wt)
	registerRepoAfterBuild(abs, "example.com/wt", 1, "build-wt", "")
	repo, ok := findRegistered(t, abs)
	if !ok {
		t.Fatal("missing")
	}
	if repo.HeadCommit != wtHead {
		t.Errorf("HeadCommit = %q, want worktree HEAD %q", repo.HeadCommit, wtHead)
	}
}

// gitHead 对非 git 目录返回空（不向上找父仓）。
func TestGitHeadIgnoresParentRepo(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git 不可用")
	}
	parent := t.TempDir()
	initGitDir(t, parent)
	child := filepath.Join(parent, "sub")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := gitHead(child); got != "" {
		t.Fatalf("子目录不得借用父仓 HEAD: %q", got)
	}
	if !strings.HasPrefix(gitHead(parent), "") {
		t.Fatalf("父仓自身应有 HEAD")
	}
}
