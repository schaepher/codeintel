package action

// P1-03（构建流程收口）：变更检测/工作区指纹从 cli 迁 action 后随迁的
// 测试——原 internal/cli/{update,stale_fingerprint,ignore_flag}_test.go
// 中的纯函数用例（Q254b 指纹口径、增量检测口径、忽略目录过滤）。

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// gitRun 在目录执行 git（注入身份，避免 CI 缺配置）。
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// gitOut 执行 git 并返回 stdout（trimmed）。
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// writeFile 写测试文件（自动建目录）。
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDetectChangedGoFilesIndexStale：索引 commit 落后于 HEAD 且工作区干净
// ——git diff HEAD 检测不到，须用 buildSHA..HEAD 的 diff 补。
func TestDetectChangedGoFilesIndexStale(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q")
	writeFile(t, filepath.Join(dir, "a.go"), "package a\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "c1")
	base := gitOut(t, dir, "rev-parse", "HEAD")

	// 模拟索引基于 c1
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := sqlite.NewRepo(db).Save(&domain.BuildMeta{BuildID: "b1", CommitSHA: base,
		ToolName: "all", Status: domain.BuildSuccess}); err != nil {
		t.Fatal(err)
	}
	db.Close()

	// c2：改 a.go + 新增 b.go，提交后工作区干净
	writeFile(t, filepath.Join(dir, "a.go"), "package a\n\nfunc F() {}\n")
	writeFile(t, filepath.Join(dir, "b.go"), "package a\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "c2")

	changed, err := DetectChangedGoFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(changed, ","); got != "a.go,b.go" {
		t.Fatalf("stale detect = %v, want a.go,b.go", changed)
	}
}

// TestDetectChangedGoFiles 未跟踪 / 修改 / 删除 / 非 go / 干净 五种形态。
func TestDetectChangedGoFiles(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q")
	writeFile(t, filepath.Join(dir, "a.go"), "package a\n")
	if changed, err := DetectChangedGoFiles(dir); err != nil || len(changed) != 1 || changed[0] != "a.go" {
		t.Fatalf("untracked detect = %v, %v", changed, err)
	}
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "init")
	writeFile(t, filepath.Join(dir, "a.go"), "package a\n\nfunc F() {}\n")
	if changed, err := DetectChangedGoFiles(dir); err != nil || len(changed) != 1 || changed[0] != "a.go" {
		t.Fatalf("modified detect = %v, %v", changed, err)
	}
	if err := os.Remove(filepath.Join(dir, "a.go")); err != nil {
		t.Fatal(err)
	}
	if changed, err := DetectChangedGoFiles(dir); err != nil || len(changed) != 1 || changed[0] != "a.go" {
		t.Fatalf("deleted detect = %v, %v", changed, err)
	}
	writeFile(t, filepath.Join(dir, "notes.md"), "x")
	changed, _ := DetectChangedGoFiles(dir)
	for _, f := range changed {
		if strings.HasSuffix(f, ".md") {
			t.Errorf("非 go 文件不应纳入: %v", changed)
		}
	}
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "cleanup")
	if changed, err := DetectChangedGoFiles(dir); err != nil || len(changed) != 0 {
		t.Fatalf("clean detect = %v, %v", changed, err)
	}
}

// TestDetectChangedGoFilesNotGit 非 git 仓库报错（update 据此提示）。
func TestDetectChangedGoFilesNotGit(t *testing.T) {
	if _, err := DetectChangedGoFiles(t.TempDir()); err == nil || !strings.Contains(err.Error(), "git") {
		t.Errorf("非 git 仓库 err = %v", err)
	}
}

// TestDetectChangedGoFilesSince --base 变更检测：diff base..HEAD + 工作区。
func TestDetectChangedGoFilesSince(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q")
	writeFile(t, filepath.Join(dir, "a.go"), "package a\n")
	writeFile(t, filepath.Join(dir, "b.go"), "package b\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "c1")
	base := gitOut(t, dir, "rev-parse", "HEAD")
	writeFile(t, filepath.Join(dir, "b.go"), "package b\n\nfunc B2() {}\n")
	writeFile(t, filepath.Join(dir, "c.go"), "package c\n")
	changed, err := DetectChangedGoFilesSince(dir, base)
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

// TestWorktreeFingerprintContentOnly 指纹只随内容变（mtime 无关），
// 文件删除也有稳定指纹。
func TestWorktreeFingerprintContentOnly(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q")
	writeFile(t, filepath.Join(dir, "a.go"), "package m\n\nfunc a() {}\n")
	files := []string{"a.go"}
	first := WorktreeFingerprint(dir, files)
	fi, err := os.Stat(filepath.Join(dir, "a.go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(dir, "a.go"), fi.ModTime().Add(-time.Hour), fi.ModTime().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if second := WorktreeFingerprint(dir, files); second != first {
		t.Fatal("mtime 变化不应改变指纹")
	}
	writeFile(t, filepath.Join(dir, "a.go"), "package m\n\nfunc a() { println(1) }\n")
	if third := WorktreeFingerprint(dir, files); third == first {
		t.Fatal("内容变化必须改变指纹")
	}
	if fourth := WorktreeFingerprint(dir, []string{"gone.go"}); fourth == "" {
		t.Fatal("删除文件也应有指纹（missing）")
	}
}

// TestWorktreeFingerprintEmpty 无变更集合 → 空指纹（调用方据此判定新鲜）。
func TestWorktreeFingerprintEmpty(t *testing.T) {
	if fp := WorktreeFingerprint(t.TempDir(), nil); fp != "" {
		t.Fatalf("无变更应为空指纹，得到 %q", fp)
	}
}

// TestFilterIgnoredFiles 增量检测/指纹过滤忽略目录内的文件。
func TestFilterIgnoredFiles(t *testing.T) {
	files := []string{"vendor/a.go", "pkg/b.go", "third_party/gen/c.go", "go.mod"}
	got := FilterIgnoredFiles(files, []string{"vendor", "third_party"})
	if strings.Join(got, ",") != "pkg/b.go,go.mod" {
		t.Fatalf("FilterIgnoredFiles = %v", got)
	}
	if all := FilterIgnoredFiles(files, nil); strings.Join(all, ",") != strings.Join(files, ",") {
		t.Fatalf("无忽略目录应原样返回")
	}
}

// TestDirtyGoFilesExcludesCommitDiff 指纹口径（Q254b）：DirtyGoFiles 只含
// 工作区相对 HEAD 的脏文件，不含"索引 commit 落后"的提交差异——
// DetectChangedGoFiles 才包含后者。
func TestDirtyGoFilesExcludesCommitDiff(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q")
	writeFile(t, filepath.Join(dir, "a.go"), "package m\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "c1")
	c1 := gitOut(t, dir, "rev-parse", "HEAD")
	// 索引基于 c1
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := sqlite.NewRepo(db).Save(&domain.BuildMeta{BuildID: "b1", CommitSHA: c1,
		ToolName: "all", Status: domain.BuildSuccess}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	// HEAD 前进（committed.go）+ 工作区脏（a.go）
	writeFile(t, filepath.Join(dir, "committed.go"), "package m\n\nfunc c() {}\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "c2")
	writeFile(t, filepath.Join(dir, "a.go"), "package m\n\nfunc a() {}\n")

	changed, err := DetectChangedGoFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	dirty, err := DirtyGoFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(changed, ",") != "a.go,committed.go" {
		t.Errorf("changed = %v, want a.go,committed.go（含提交差异）", changed)
	}
	if len(dirty) != 1 || dirty[0] != "a.go" {
		t.Errorf("dirty = %v, want [a.go]（只工作区）", dirty)
	}
}
