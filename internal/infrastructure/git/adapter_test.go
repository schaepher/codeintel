package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
)

// newGitRepo 在临时目录建 git 仓库并提交两个 commit。
func newGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "a.go")
	run("commit", "-q", "-m", "first commit")
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte("package b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "b.go")
	run("commit", "-q", "-m", "second commit")
	return dir
}

func TestIndexGitHistory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found")
	}
	dir := newGitRepo(t)
	var nodes []*domain.CodeEntity
	var facts []*domain.Fact
	adapter := &Adapter{}
	err := adapter.Index(context.Background(), &domain.Repository{Path: dir}, nil, func(item domain.Item) error {
		if item.Node != nil {
			nodes = append(nodes, item.Node)
		}
		if item.Fact != nil {
			facts = append(facts, item.Fact)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	// 2 个 commit 节点（shortSHA 12 位）
	if len(nodes) != 2 {
		t.Fatalf("commit nodes = %d, want 2", len(nodes))
	}
	for _, n := range nodes {
		if n.Kind != domain.KindCommit {
			t.Errorf("node kind = %s", n.Kind)
		}
		if len(n.Name) != 12 {
			t.Errorf("short sha = %q", n.Name)
		}
	}
	// 2 条 modified_by 边（a.go、b.go → 各自 commit）
	if len(facts) != 2 {
		t.Fatalf("modified_by edges = %d, want 2", len(facts))
	}
	for _, f := range facts {
		if f.Kind != domain.FactModifiedBy || f.Confidence != 1.0 || f.ToolSource != domain.ToolGit {
			t.Errorf("fact = %+v", f)
		}
		if !filepath.HasPrefix(string(f.SourceID), "file:") {
			t.Errorf("source = %s", f.SourceID)
		}
	}
	// 提交信息（second commit 的 message）
	msgs := map[string]bool{}
	for _, n := range nodes {
		if m, ok := n.Properties["message"].(string); ok {
			msgs[m] = true
		}
	}
	if !msgs["first commit"] || !msgs["second commit"] {
		t.Errorf("commit messages = %v", msgs)
	}
	// 日期字段存在
	if _, ok := nodes[0].Properties["date"]; !ok {
		t.Error("commit should carry date")
	}
}

func TestIndexNonGitDir(t *testing.T) {
	// 非 git 目录：git log 失败 → 返回错误
	// R67：TMPDIR 指向仓库 .tmp（git 仓库内）时 t.TempDir() 有 git 祖先
	// ——"非 git 目录"场景无法构造，跳过（原 R28 假失败根因）
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "rev-parse", "--git-dir").Output(); err == nil && len(out) > 0 {
		t.Skip("TMPDIR 在 git 仓库内——无法构造非 git 目录（R67）")
	}
	adapter := &Adapter{}
	err := adapter.Index(context.Background(), &domain.Repository{Path: dir}, nil, func(domain.Item) error { return nil })
	if err == nil {
		t.Error("Index on non-git dir should fail")
	}
}

func TestIndexMaxCommits(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found")
	}
	dir := newGitRepo(t)
	// MaxCommits=1：只取最近一个 commit
	adapter := &Adapter{MaxCommits: 1}
	count := 0
	err := adapter.Index(context.Background(), &domain.Repository{Path: dir}, nil, func(item domain.Item) error {
		if item.Node != nil {
			count++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if count != 1 {
		t.Errorf("commits with MaxCommits=1 = %d, want 1", count)
	}
}

func TestNormalizePath(t *testing.T) {
	cases := map[string]string{
		"a.go":                             "a.go",
		`"renamed.go"`:                     "renamed.go",
		"{old => new}/file.go":             "new/file.go",
		"dir/{old.go => new.go}":           "new.go",
		"  spaced.go  ":                    "spaced.go",
		`"dir/a.go"`:                       "dir/a.go",
		"{src => dst}/internal/app/app.go": "dst/internal/app/app.go",
	}
	for in, want := range cases {
		if got := normalizePath(in); got != want {
			t.Errorf("normalizePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestShortSHA(t *testing.T) {
	sha := "0123456789abcdef0123456789abcdef"
	if got := shortSHA(sha); got != "0123456789ab" {
		t.Errorf("shortSHA = %q", got)
	}
	if got := shortSHA("short"); got != "short" {
		t.Errorf("shortSHA(short) = %q", got)
	}
}

// gitRecordProgress 记录型进度实现（进度接线断言用）。
type gitRecordProgress struct {
	begun  []string
	adv    []int
	ended  []string
	totals []int
}

func (p *gitRecordProgress) Begin(name string, depth, total int) {
	p.begun = append(p.begun, name)
	p.totals = append(p.totals, total)
}
func (p *gitRecordProgress) Advance(name string, done int) { p.adv = append(p.adv, done) }
func (p *gitRecordProgress) End(name string, _ time.Duration, _ error) {
	p.ended = append(p.ended, name)
}
func (p *gitRecordProgress) Finish() {}

// TestGitIndexReportsProgress：git 适配器上报真实提交数（分母）与逐提交
// 进度（不编造分母）。
func TestGitIndexReportsProgress(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found")
	}
	dir := newGitRepo(t)
	rec := &gitRecordProgress{}
	adapter := &Adapter{}
	adapter.SetProgress(rec)
	if err := adapter.Index(context.Background(), &domain.Repository{Path: dir}, nil, func(domain.Item) error { return nil }); err != nil {
		t.Fatalf("Index: %v", err)
	}
	if len(rec.begun) != 1 || rec.begun[0] != "git commits" {
		t.Fatalf("应上报 git commits 步骤: %+v", rec.begun)
	}
	if rec.totals[0] != 2 {
		t.Fatalf("分母应为提交数 2，得到 %d", rec.totals[0])
	}
	if len(rec.adv) != 2 || rec.adv[len(rec.adv)-1] != 2 {
		t.Fatalf("应逐提交 Advance 到 2: %v", rec.adv)
	}
	if len(rec.ended) != 1 || rec.ended[0] != "git commits" {
		t.Fatalf("应结束 git commits: %v", rec.ended)
	}
}

// TestGitIndexIgnoreDirs：--ignore-dir 目录内的文件不建 modified_by 边。
func TestGitIndexIgnoreDirs(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found")
	}
	dir := newGitRepo(t)
	if err := os.MkdirAll(filepath.Join(dir, "vendor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vendor", "v.go"), []byte("package v\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := exec.Command("git", "-C", dir, "add", "vendor/v.go")
	run.Run()
	run = exec.Command("git", "-C", dir, "commit", "-q", "-m", "vendor file")
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
	var facts []*domain.Fact
	adapter := &Adapter{}
	err := adapter.Index(context.Background(), &domain.Repository{Path: dir, IgnoreDirs: []string{"vendor"}}, nil,
		func(item domain.Item) error {
			if item.Fact != nil {
				facts = append(facts, item.Fact)
			}
			return nil
		})
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	for _, f := range facts {
		if string(f.SourceID) == "file:vendor/v.go" {
			t.Fatalf("忽略目录文件不应建边: %v", f)
		}
	}
}
