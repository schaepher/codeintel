package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// Q254b：stale 判定改用「只算会进索引的变更 + 工作区内容指纹」——本文件覆盖
// 两个历史误报场景与新语义。基础场景（SHA 前进 / 无构建记录 / 老时间戳回退）
// 见 stale_test.go。

// saveStaleMeta 写一条带指纹的构建记录（staleInfo 只读 build_metadata）。
func saveStaleMeta(t *testing.T, r *sqlite.Repo, sha, fingerprint string) {
	t.Helper()
	if err := r.Save(&domain.BuildMeta{
		BuildID: "b1", CommitSHA: sha, ToolName: "all", Status: domain.BuildSuccess,
		WorktreeFingerprint: fingerprint,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

func openStaleRepo(t *testing.T, dir string) *sqlite.Repo {
	t.Helper()
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return sqlite.NewRepo(db)
}

// 误报①：非 Go 文件的改动不算"未索引"。
func TestStaleInfoIgnoresNonGoChanges(t *testing.T) {
	dir := seedGitRepo(t)
	sha := strings.TrimSpace(gitRunOut(t, dir, "rev-parse", "HEAD"))
	r := openStaleRepo(t, dir)
	saveStaleMeta(t, r, sha, "")
	writeTestFile(t, filepath.Join(dir, "README.md"), "# changed\n")
	writeTestFile(t, filepath.Join(dir, "notes.txt"), "x\n")
	if got := staleInfo(dir, r); got != "" {
		t.Fatalf("非 Go 文件改动不应报过期，得到：%s", got)
	}
}

// 误报②：改 Go 文件后**重索引**（构建时已含该变更）→ 不再报过期。
func TestStaleInfoFreshWhenDirtyGoFileIndexed(t *testing.T) {
	dir := seedGitRepo(t)
	sha := strings.TrimSpace(gitRunOut(t, dir, "rev-parse", "HEAD"))
	writeTestFile(t, filepath.Join(dir, "main.go"), "package m\n\nfunc main() { println(1) }\n")
	changed, err := detectChangedGoFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) == 0 {
		t.Fatal("fixture 应有 1 个变更 Go 文件")
	}
	fp := worktreeFingerprint(dir, changed)
	if fp == "" {
		t.Fatal("指纹不应为空")
	}
	r := openStaleRepo(t, dir)
	saveStaleMeta(t, r, sha, fp)
	if got := staleInfo(dir, r); got != "" {
		t.Fatalf("变更已在构建时索引，不应报过期，得到：%s", got)
	}
}

// 构建后再次改动该 Go 文件（内容变）→ 必须报过期。
func TestStaleInfoStaleAfterGoFileChangedPostBuild(t *testing.T) {
	dir := seedGitRepo(t)
	sha := strings.TrimSpace(gitRunOut(t, dir, "rev-parse", "HEAD"))
	writeTestFile(t, filepath.Join(dir, "main.go"), "package m\n\nfunc main() { println(1) }\n")
	changed, _ := detectChangedGoFiles(dir)
	r := openStaleRepo(t, dir)
	saveStaleMeta(t, r, sha, worktreeFingerprint(dir, changed))
	writeTestFile(t, filepath.Join(dir, "main.go"), "package m\n\nfunc main() { println(2) }\n")
	got := staleInfo(dir, r)
	if got == "" {
		t.Fatal("构建后又改动 Go 文件，必须报过期")
	}
	if !strings.Contains(got, "1 个 Go 文件") || !strings.Contains(got, "已改") {
		t.Fatalf("提示应点明数量与原因：%s", got)
	}
}

// 新增未跟踪 Go 文件 → 报过期。
func TestStaleInfoStaleWhenNewGoFileAdded(t *testing.T) {
	dir := seedGitRepo(t)
	sha := strings.TrimSpace(gitRunOut(t, dir, "rev-parse", "HEAD"))
	r := openStaleRepo(t, dir)
	saveStaleMeta(t, r, sha, worktreeFingerprint(dir, nil))
	writeTestFile(t, filepath.Join(dir, "extra.go"), "package m\n\nfunc extra() {}\n")
	got := staleInfo(dir, r)
	if got == "" || !strings.Contains(got, "1 个 Go 文件") {
		t.Fatalf("新增 Go 文件应报过期：%q", got)
	}
}

// 指纹只随内容变（mtime 无关），文件删除也有稳定指纹。
func TestWorktreeFingerprintContentOnly(t *testing.T) {
	dir := seedGitRepo(t)
	writeTestFile(t, filepath.Join(dir, "a.go"), "package m\n\nfunc a() {}\n")
	files := []string{"a.go"}
	first := worktreeFingerprint(dir, files)
	fi, err := os.Stat(filepath.Join(dir, "a.go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(dir, "a.go"), fi.ModTime().Add(-time.Hour), fi.ModTime().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if second := worktreeFingerprint(dir, files); second != first {
		t.Fatal("mtime 变化不应改变指纹")
	}
	writeTestFile(t, filepath.Join(dir, "a.go"), "package m\n\nfunc a() { println(1) }\n")
	if third := worktreeFingerprint(dir, files); third == first {
		t.Fatal("内容变化必须改变指纹")
	}
	if fourth := worktreeFingerprint(dir, []string{"gone.go"}); fourth == "" {
		t.Fatal("删除文件也应有指纹（missing）")
	}
}

// 无变更集合 → 空指纹（调用方据此判定"新鲜"）。
func TestWorktreeFingerprintEmpty(t *testing.T) {
	if fp := worktreeFingerprint(t.TempDir(), nil); fp != "" {
		t.Fatalf("无变更应为空指纹，得到 %q", fp)
	}
}

// 回归（真实误报）：构建时索引**落后 HEAD**（集合里多出"提交差异"文件）→
// 重索引后同一函数只返回工作区脏文件 → 用 detectChangedGoFiles 做指纹会
// 永不匹配。指纹必须用 dirtyGoFiles（相对 HEAD）。
func TestStaleInfoFreshAfterReindexWhenIndexWasBehind(t *testing.T) {
	dir := seedGitRepo(t)
	sha1 := strings.TrimSpace(gitRunOut(t, dir, "rev-parse", "HEAD"))
	// 旧构建记录（索引落后）
	r := openStaleRepo(t, dir)
	saveStaleMeta(t, r, sha1, "")
	// 提交一个 Go 变更（HEAD 前进）+ 工作区再改一个文件（脏）
	writeTestFile(t, filepath.Join(dir, "committed.go"), "package m\n\nfunc c() {}\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-qm", "second")
	sha2 := strings.TrimSpace(gitRunOut(t, dir, "rev-parse", "HEAD"))
	writeTestFile(t, filepath.Join(dir, "main.go"), "package m\n\nfunc main() { println(9) }\n")

	changed, err := detectChangedGoFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	dirty, err := dirtyGoFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	// 口径差异：changed 含提交差异（committed.go），dirty 只含工作区脏文件
	if len(changed) != 2 || len(dirty) != 1 || dirty[0] != "main.go" {
		t.Fatalf("口径不符：changed=%v dirty=%v", changed, dirty)
	}
	// 模拟重索引后的记录：sha=HEAD + 工作区指纹（dirtyGoFiles 口径）
	saveStaleMeta(t, r, sha2, worktreeFingerprint(dir, dirty))
	if got := staleInfo(dir, r); got != "" {
		t.Fatalf("重索引后不应报过期（否则就是口径漂移回归）：%s", got)
	}
}
