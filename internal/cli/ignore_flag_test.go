package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestNormalizeIgnoreDir：忽略目录归一与非法输入拒绝。
func TestNormalizeIgnoreDir(t *testing.T) {
	ok := map[string]string{
		"vendor":          "vendor",
		"./vendor/":       "vendor",
		"third_party/gen": "third_party/gen",
		"a\\b":            "a/b",
	}
	for in, want := range ok {
		got, err := normalizeIgnoreDir(in)
		if err != nil || got != want {
			t.Errorf("normalizeIgnoreDir(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", ".", "/abs/path", "../out"} {
		if _, err := normalizeIgnoreDir(bad); err == nil {
			t.Errorf("normalizeIgnoreDir(%q) 应报错", bad)
		}
	}
}

// TestConfigIgnoreDirAddRmList：config ignore-dir add/rm/list 写读
// 仓库级 codeintel.yaml（buildRepo 自动应用）。
func TestConfigIgnoreDirAddRmList(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/root\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := cmdConfigIgnoreDir([]string{"add", "vendor", "third_party/gen", "--repo", dir}); code != 0 {
		t.Fatalf("add exit = %d", code)
	}
	// 去重 + 归一
	if code := cmdConfigIgnoreDir([]string{"add", "./vendor/", "--repo", dir}); code != 0 {
		t.Fatalf("add dup exit = %d", code)
	}
	got := loadIgnoreDirs(dir)
	want := []string{"third_party/gen", "vendor"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loadIgnoreDirs = %v; want %v", got, want)
	}
	// buildRepo 自动应用
	repo, err := buildRepo(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(repo.IgnoreDirs, want) {
		t.Fatalf("buildRepo.IgnoreDirs = %v; want %v", repo.IgnoreDirs, want)
	}
	// rm
	if code := cmdConfigIgnoreDir([]string{"rm", "vendor", "--repo", dir}); code != 0 {
		t.Fatalf("rm exit = %d", code)
	}
	if got := loadIgnoreDirs(dir); !reflect.DeepEqual(got, []string{"third_party/gen"}) {
		t.Fatalf("rm 后 = %v", got)
	}
	// 清空 → 删除文件
	if code := cmdConfigIgnoreDir([]string{"rm", "third_party/gen", "--repo", dir}); code != 0 {
		t.Fatalf("rm last exit = %d", code)
	}
	if _, err := os.Stat(ignoreConfigPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("清空后应删除 codeintel.yaml")
	}
}

// TestBuildRepoAutoIgnore：buildRepo 未显式传 ignoreDirs 时读 codeintel.yaml，
// 且忽略目录内的 module 不被发现。
func TestBuildRepoAutoIgnore(t *testing.T) {
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
	if err := saveIgnoreDirs(dir, []string{"ignored"}); err != nil {
		t.Fatal(err)
	}
	repo, err := buildRepo(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range repo.Modules {
		if m == "example.com/ignored" {
			t.Fatalf("忽略目录内的 module 不应被发现: %v", repo.Modules)
		}
	}
}

// TestFilterIgnoredFiles：增量检测/指纹过滤忽略目录内的文件。
func TestFilterIgnoredFiles(t *testing.T) {
	files := []string{"vendor/a.go", "pkg/b.go", "third_party/gen/c.go", "go.mod"}
	got := filterIgnoredFiles(files, []string{"vendor", "third_party"})
	want := []string{"pkg/b.go", "go.mod"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterIgnoredFiles = %v; want %v", got, want)
	}
	if all := filterIgnoredFiles(files, nil); !reflect.DeepEqual(all, files) {
		t.Fatalf("无忽略目录应原样返回")
	}
}
