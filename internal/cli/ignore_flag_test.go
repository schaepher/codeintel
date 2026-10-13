package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestStringListFlag：--ignore-dir 可重复（flag.Value 追加语义）。
func TestStringListFlag(t *testing.T) {
	var s stringList
	if err := s.Set("vendor"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("third_party/gen"); err != nil {
		t.Fatal(err)
	}
	if len(s) != 2 || s[0] != "vendor" || s[1] != "third_party/gen" {
		t.Fatalf("stringList = %v", s)
	}
	// 空值忽略
	_ = s.Set("  ")
	if len(s) != 2 {
		t.Fatalf("空值不应追加: %v", s)
	}
}

// TestBuildRepoIgnoreDirs：buildRepo 把忽略目录写入 Repository 且其中的
// module 不被发现。
func TestBuildRepoIgnoreDirs(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/root\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "ignored"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ignored", "go.mod"), []byte("module example.com/ignored\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo, err := buildRepo(dir, "ignored")
	if err != nil {
		t.Fatal(err)
	}
	if len(repo.IgnoreDirs) != 1 || repo.IgnoreDirs[0] != "ignored" {
		t.Fatalf("IgnoreDirs = %v", repo.IgnoreDirs)
	}
	for _, m := range repo.Modules {
		if m == "example.com/ignored" {
			t.Fatalf("忽略目录内的 module 不应被发现: %v", repo.Modules)
		}
	}
}
