//go:build integration

package integration

// todo25 §10.1：真实 git worktree/submodule 的构建身份回归。

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

func todo25ModuleRepo(t *testing.T, dir, module string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "go.mod"), "module "+module+"\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main(){}\n")
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "init")
}

// execGit 执行 git -C dir …，返回 trim 后的 stdout。
func execGit(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}

func todo25GitHead(t *testing.T, dir string) string {
	t.Helper()
	out, err := execGit(dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse %s: %v", dir, err)
	}
	return out
}

func todo25RegistryEntry(t *testing.T, path string) sqlite.RegistryRepo {
	t.Helper()
	r, err := sqlite.OpenRegistry(os.Getenv("CODEINTEL_REGISTRY_DIR"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	repos, err := r.ListRepos()
	if err != nil {
		t.Fatal(err)
	}
	for _, rp := range repos {
		if filepath.Clean(rp.Path) == filepath.Clean(path) {
			return rp
		}
	}
	t.Fatalf("注册表缺少 %s", path)
	return sqlite.RegistryRepo{}
}

func todo25BuildID(t *testing.T, path string) (registryID, metaID, head, status string) {
	t.Helper()
	reg := todo25RegistryEntry(t, path)
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	meta, err := sqlite.NewRepo(db).GetLatest()
	if err != nil {
		t.Fatal(err)
	}
	return reg.BuildID, meta.BuildID, reg.HeadCommit, string(meta.Status)
}

func TestTodo25BuildIdentityWorktreeSubmodule(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	root := t.TempDir()
	mainRepo := filepath.Join(root, "main")
	todo25ModuleRepo(t, mainRepo, "example.com/wtmain")
	mainHead := todo25GitHead(t, mainRepo)

	// worktree：自身 .git 文件；先提交一个新 HEAD 再构建。
	wt := filepath.Join(root, "wt")
	if _, err := execGit(mainRepo, "worktree", "add", "-b", "feat", wt); err != nil {
		t.Fatalf("worktree add: %v", err)
	}
	writeFile(t, filepath.Join(wt, "extra.go"), "package main\n\nfunc extra(){}\n")
	gitRun(t, wt, "add", "-A")
	gitRun(t, wt, "commit", "-m", "wt")
	wtHead := todo25GitHead(t, wt)
	if wtHead == mainHead {
		t.Fatal("worktree HEAD 应与父仓库不同")
	}

	// 本地文件型 submodule：独立仓库 + 父仓库登记。
	subMain := filepath.Join(root, "subsrc")
	todo25ModuleRepo(t, subMain, "example.com/wtsub")
	subHead := todo25GitHead(t, subMain)
	if subHead == mainHead {
		t.Fatal("submodule HEAD 应不同（不同提交内容）")
	}
	if _, err := execGit(mainRepo, "-c", "protocol.file.allow=always", "submodule", "add", subMain, "sub"); err != nil {
		t.Fatalf("submodule add: %v", err)
	}
	gitRun(t, mainRepo, "commit", "-am", "add submodule")
	sub := filepath.Join(mainRepo, "sub")

	// 无自身 .git 的相邻模块：HeadCommit 必须为空（不继承父仓库）。
	plain := filepath.Join(mainRepo, "plainmod")
	writeFile(t, filepath.Join(plain, "go.mod"), "module example.com/plainmod\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(plain, "p.go"), "package main\n\nfunc main(){}\n")

	for _, d := range []string{wt, sub, plain} {
		clearIndex(t, d)
	}
	build := func(d string) {
		t.Helper()
		if code := runCLI(t, "init", "--repo", d, "--workers", "1", "--progress", "none"); code != 0 {
			t.Fatalf("init %s exit=%d", d, code)
		}
	}
	build(wt)
	firstWT := todo25RegistryEntry(t, wt)
	bid1, meta1, head1, status1 := todo25BuildID(t, wt)
	if bid1 == "" || bid1 != meta1 || head1 != wtHead || status1 != "success" {
		t.Fatalf("worktree 首次构建身份不符: reg=%q meta=%q head=%q status=%q",
			bid1, meta1, head1, status1)
	}
	build(wt)
	bid2, meta2, head2, _ := todo25BuildID(t, wt)
	if bid2 == "" || bid2 != meta2 || head2 != wtHead || bid2 == bid1 {
		t.Fatalf("worktree 第二次构建身份/唯一性不符: %q/%q head=%q（首次 %q）",
			bid2, meta2, head2, bid1)
	}
	if code := runCLI(t, "update", "--repo", wt, "--progress", "none"); code != 0 {
		t.Fatalf("update worktree exit=%d", code)
	}
	bid3, meta3, head3, _ := todo25BuildID(t, wt)
	if bid3 == "" || bid3 != meta3 || head3 != wtHead {
		t.Fatalf("worktree update 身份不符: %q/%q head=%q", bid3, meta3, head3)
	}
	afterWT := todo25RegistryEntry(t, wt)
	if afterWT.RegisteredAt != firstWT.RegisteredAt {
		t.Errorf("RegisteredAt 不应变化: %q → %q", firstWT.RegisteredAt, afterWT.RegisteredAt)
	}

	// submodule：HeadCommit 取子模块自身 HEAD（≠ 父仓库 HEAD）。
	build(sub)
	bidSub, metaSub, headSub, _ := todo25BuildID(t, sub)
	if bidSub == "" || bidSub != metaSub || headSub != subHead || headSub == mainHead {
		t.Fatalf("submodule 身份不符: %q/%q head=%q（want %q）", bidSub, metaSub, headSub, subHead)
	}

	// 无自身 .git 的相邻模块：HeadCommit 为空。
	build(plain)
	bidPlain, metaPlain, headPlain, _ := todo25BuildID(t, plain)
	if bidPlain == "" || bidPlain != metaPlain || headPlain != "" {
		t.Fatalf("无 .git 相邻模块应 HeadCommit 为空: %q/%q head=%q",
			bidPlain, metaPlain, headPlain)
	}
}
