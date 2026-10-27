package cli

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// gitRun 在指定目录执行 git（注入 user 配置供 commit 使用）。
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// gitRunOut 执行 git 并返回 stdout（trimmed）。
func gitRunOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestCmdUpdateNoChanges：update 成功路径的轻量分支——git 仓库无变更
// .go 文件时直接提示已最新（exit 0），不触发索引构建（无 scip-go 依赖）。
func TestCmdUpdateNoChanges(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/m\n\ngo 1.21\n")
	writeTestFile(t, filepath.Join(dir, "a.go"), "package a\n")
	gitRun(t, dir, "init", "-q")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "init")

	out := captureStdout(func() {
		if code := cmdUpdate(nil, []string{"--repo", dir}); code != 0 {
			t.Errorf("update no-change exit = %d", code)
		}
	})
	if !strings.Contains(out, "无变更") {
		t.Errorf("no-change stdout = %q, want 无变更提示", out)
	}
}

// TestCmdUpdateGoModChanged：go.mod 变更影响 module 范围——提示全量 init
// 而非增量（exit 1）。
func TestCmdUpdateGoModChanged(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/m\n\ngo 1.21\n")
	gitRun(t, dir, "init", "-q")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "init")
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/m\n\ngo 1.21\n\n// bump\n")

	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	code := cmdUpdate(nil, []string{"--repo", dir})
	w.Close()
	os.Stderr = old
	var buf strings.Builder
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	if code != 1 || !strings.Contains(buf.String(), "codeintel init") {
		t.Errorf("go.mod changed = %d, stderr=%q（应提示全量 init）", code, buf.String())
	}
}

func TestCmdUpdateNoGit(t *testing.T) {
	// 非 git 仓库：增量更新报错提示
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/m\n\ngo 1.21\n")
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	code := cmdUpdate(nil, []string{"--repo", dir})
	w.Close()
	os.Stderr = old
	var buf strings.Builder
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	if code != 1 || !strings.Contains(buf.String(), "git") {
		t.Errorf("update without git = %d, stderr=%q", code, buf.String())
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCmdUpdateBase：--base 分层端到端——base 目录索引物化到本地
// （未变更包数据直接复用）+ 只分析 diff(base..HEAD) 的变更包。
func TestCmdUpdateBase(t *testing.T) {
	repo := t.TempDir()
	gitRun(t, repo, "init", "-q")
	writeTestFile(t, filepath.Join(repo, "go.mod"), "module example.com/m\n\ngo 1.21\n")
	writeTestFile(t, filepath.Join(repo, "a.go"), "package m\n\nfunc A() {}\n")
	writeTestFile(t, filepath.Join(repo, "b.go"), "package m\n\nfunc B() {}\n")
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-q", "-m", "c1")
	c1 := gitRunOut(t, repo, "rev-parse", "HEAD")

	// clone 两份 workspace（同历史——baseCommit 在 ws 历史中存在）
	baseDir := filepath.Join(t.TempDir(), "base")
	wsDir := filepath.Join(t.TempDir(), "ws")
	gitRun(t, repo, "clone", "-q", repo, baseDir)
	gitRun(t, repo, "clone", "-q", repo, wsDir)

	// base 索引（手动造：节点 + 边 + build_metadata commit=c1）
	baseDB, err := sqlite.Open(baseDir)
	if err != nil {
		t.Fatal(err)
	}
	baseR := sqlite.NewRepo(baseDB)
	if _, err := baseR.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:A", Kind: domain.KindFunction, Name: "A", FilePath: "a.go", LineStart: 3},
		{ID: "symbol:go:example.com/m:B", Kind: domain.KindFunction, Name: "B", FilePath: "b.go", LineStart: 3},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := baseR.Save(&domain.BuildMeta{BuildID: "b1", CommitSHA: c1, ToolName: "all", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	baseDB.Close()

	// ws 改 b.go（新增函数 B2）→ commit c2
	writeTestFile(t, filepath.Join(wsDir, "b.go"), "package m\n\nfunc B() {}\n\nfunc B2() {}\n")
	gitRun(t, wsDir, "add", "-A")
	gitRun(t, wsDir, "commit", "-q", "-m", "c2")

	// cmdUpdate --base：物化 + 按包增量
	captureStdout(func() {
		if code := cmdUpdate(context.Background(), []string{"--repo", wsDir, "--base", baseDir}); code != 0 {
			t.Fatalf("cmdUpdate --base exit = %d", code)
		}
	})

	// 断言：未变更的 A（物化来源）+ 变更包新函数 B2（增量分析）
	db, err := sqlite.Open(wsDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	if n, err := r.GetSymbol("symbol:go:example.com/m:A"); err != nil || n.Name != "A" {
		t.Errorf("物化的 A 应存在: %+v, %v", n, err)
	}
	if n, err := r.GetSymbol("symbol:go:example.com/m:B2"); err != nil || n.Name != "B2" {
		t.Errorf("增量分析的 B2 应存在: %+v, %v", n, err)
	}
	// base.txt 配置已记录
	db2, err := sqlite.Open(wsDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := db2.BasePath(); got != baseDir {
		t.Errorf("base.txt = %q; want %q", got, baseDir)
	}
	db2.Close()
}
