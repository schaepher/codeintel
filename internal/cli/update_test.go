package cli

import (
	"context"
	"database/sql"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
	_ "modernc.org/sqlite"
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

// TestDetectChangedGoFilesIndexStale：索引 commit 落后于 HEAD 且工作区
// 干净——git diff HEAD 检测不到（update 误报"无变更"），须用
// buildSHA..HEAD 的 diff 补（真实触发：索引停在 609bd65、HEAD 7994bf3）。
func TestDetectChangedGoFilesIndexStale(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q")
	writeTestFile(t, filepath.Join(dir, "a.go"), "package a\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "c1")
	base := gitRunOut(t, dir, "rev-parse", "HEAD")

	// 模拟索引基于 c1（.codeintel/codeintel.db + build_metadata）
	idxDir := filepath.Join(dir, ".codeintel")
	writeTestFile(t, filepath.Join(idxDir, "keep"), "")
	db, err := sql.Open("sqlite", "file:"+filepath.Join(idxDir, "codeintel.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE build_metadata (build_id TEXT PRIMARY KEY, commit_sha TEXT, timestamp INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO build_metadata VALUES ('b1', ?, 1)`, base); err != nil {
		t.Fatal(err)
	}

	// c2：改 a.go + 新增 b.go，提交后工作区干净（HEAD 前进）
	writeTestFile(t, filepath.Join(dir, "a.go"), "package a\n\nfunc F() {}\n")
	writeTestFile(t, filepath.Join(dir, "b.go"), "package a\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "c2")

	changed, err := detectChangedGoFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a.go", "b.go"}
	if strings.Join(changed, ",") != strings.Join(want, ",") {
		t.Fatalf("stale detect = %v, want %v", changed, want)
	}
}

func TestDetectChangedGoFiles(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q")
	// 未跟踪新文件
	writeTestFile(t, filepath.Join(dir, "a.go"), "package a\n")
	changed, err := detectChangedGoFiles(dir)
	if err != nil || len(changed) != 1 || changed[0] != "a.go" {
		t.Fatalf("untracked detect = %v, %v", changed, err)
	}
	// 提交后修改：diff HEAD 检测
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "init")
	writeTestFile(t, filepath.Join(dir, "a.go"), "package a\n\nfunc F() {}\n")
	changed, err = detectChangedGoFiles(dir)
	if err != nil || len(changed) != 1 || changed[0] != "a.go" {
		t.Fatalf("modified detect = %v, %v", changed, err)
	}
	// 删除文件：diff HEAD 仍列出
	if err := os.Remove(filepath.Join(dir, "a.go")); err != nil {
		t.Fatal(err)
	}
	changed, err = detectChangedGoFiles(dir)
	if err != nil || len(changed) != 1 || changed[0] != "a.go" {
		t.Fatalf("deleted detect = %v, %v", changed, err)
	}
	// 非 .go 文件不纳入
	writeTestFile(t, filepath.Join(dir, "notes.md"), "x")
	changed, _ = detectChangedGoFiles(dir)
	for _, f := range changed {
		if strings.HasSuffix(f, ".md") {
			t.Errorf("非 go 文件不应纳入: %v", changed)
		}
	}
	// 无变更 → 空
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "cleanup")
	changed, err = detectChangedGoFiles(dir)
	if err != nil || len(changed) != 0 {
		t.Fatalf("clean detect = %v, %v", changed, err)
	}
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

// TestDetectChangedGoFilesSince：--base 变更检测——diff base..HEAD +
// 工作区 + 未跟踪。
func TestDetectChangedGoFilesSince(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q")
	writeTestFile(t, filepath.Join(dir, "a.go"), "package a\n")
	writeTestFile(t, filepath.Join(dir, "b.go"), "package b\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "c1")
	base := gitRunOut(t, dir, "rev-parse", "HEAD")
	// 改 b.go + 新增 c.go（未跟踪）
	writeTestFile(t, filepath.Join(dir, "b.go"), "package b\n\nfunc B2() {}\n")
	writeTestFile(t, filepath.Join(dir, "c.go"), "package c\n")
	changed, err := detectChangedGoFilesSince(dir, base)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(changed, ",")
	for _, want := range []string{"b.go", "c.go"} {
		if !strings.Contains(got, want) {
			t.Errorf("变更文件应含 %s: %s", want, got)
		}
	}
	if strings.Contains(got, "a.go") {
		t.Errorf("未变更的 a.go 不应出现: %s", got)
	}
}
