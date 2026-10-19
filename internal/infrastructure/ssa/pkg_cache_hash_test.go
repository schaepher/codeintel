package ssa

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// naivePkgContentHash 旧实现（整文件读入后写入），作为等价性/基准对照。
func naivePkgContentHash(tb testing.TB, files []string) (string, error) {
	tb.Helper()
	h := sha256.New()
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return "", err
		}
		h.Write(data)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// TestPkgContentHashMatchesNaive：分块读取与旧整文件读取 hash 逐字节等价
// （含空文件、多文件顺序、分隔符 0x00）。
func TestPkgContentHashMatchesNaive(t *testing.T) {
	dir := t.TempDir()
	big := make([]byte, 300*1024) // > 32KB 缓冲，覆盖多次分块
	for i := range big {
		big[i] = byte(i * 31)
	}
	files := []string{
		filepath.Join(dir, "a.go"),
		filepath.Join(dir, "empty.go"),
		filepath.Join(dir, "big.go"),
	}
	if err := os.WriteFile(files[0], []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(files[1], nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(files[2], big, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := pkgContentHash(files)
	if err != nil {
		t.Fatal(err)
	}
	want, err := naivePkgContentHash(t, files)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("hash 不等价: got %s want %s", got, want)
	}
	// 顺序敏感（旧实现按文件顺序拼接）
	rev, err := pkgContentHash([]string{files[2], files[0]})
	if err != nil {
		t.Fatal(err)
	}
	if rev == got {
		t.Fatal("文件顺序变化应改变 hash")
	}
}

// TestPkgContentHashMissingFile：错误路径保持（返回 error，不 panic）。
func TestPkgContentHashMissingFile(t *testing.T) {
	if _, err := pkgContentHash([]string{filepath.Join(t.TempDir(), "nope.go")}); err == nil {
		t.Fatal("缺失文件应返回 error")
	}
}

// BenchmarkPkgContentHashStream 对照旧整文件读取：记录每文件分配。
func BenchmarkPkgContentHashStream(b *testing.B) {
	dir := b.TempDir()
	files := make([]string, 0, 40)
	content := make([]byte, 64<<10)
	for i := 0; i < 40; i++ {
		p := filepath.Join(dir, "f"+string(rune('a'+i))+".go")
		if err := os.WriteFile(p, content, 0o644); err != nil {
			b.Fatal(err)
		}
		files = append(files, p)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := pkgContentHash(files); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPkgContentHashNaive 旧实现基准（对照）。
func BenchmarkPkgContentHashNaive(b *testing.B) {
	dir := b.TempDir()
	files := make([]string, 0, 40)
	content := make([]byte, 64<<10)
	for i := 0; i < 40; i++ {
		p := filepath.Join(dir, "f"+string(rune('a'+i))+".go")
		if err := os.WriteFile(p, content, 0o644); err != nil {
			b.Fatal(err)
		}
		files = append(files, p)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := naivePkgContentHash(b, files); err != nil {
			b.Fatal(err)
		}
	}
}
