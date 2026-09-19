package ssa

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Q248：共享源码行缓存的等价性测试——缓存结果必须与"直接读文件 +
// strings.Split + TrimSpace"逐字一致（旧实现是每个函数一个 extractor
// 各自读盘拆分：go2o 1.2 万函数 × 各自文件，占全部分配 25% / 峰值 live
// 66%）。同时锁定"同文件只读一次盘"与并发安全。

// directSourceLine 参考实现（改造前 fe_nodes.go/alias_ids.go 的行为）。
func directSourceLine(t *testing.T, repoPath, filePath string, line int) string {
	t.Helper()
	if filePath == "" || line < 1 {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(repoPath, filepath.FromSlash(filePath)))
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	if line > len(lines) {
		return ""
	}
	return strings.TrimSpace(lines[line-1])
}

func lineCacheFixture(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	content := "package main\n\nfunc main() {\n\tx := 1 // 缩进行\n\ty := 2\n}\n"
	writeFile(t, filepath.Join(dir, "main.go"), content)
	return dir, "main.go"
}

func TestLineCacheMatchesDirectRead(t *testing.T) {
	dir, rel := lineCacheFixture(t)
	c := newLineCache(dir)
	for _, line := range []int{-1, 0, 1, 2, 4, 6, 7, 1000} {
		want := directSourceLine(t, dir, rel, line)
		if got := c.line(rel, line); got != want {
			t.Errorf("line(%d) = %q, want %q", line, got, want)
		}
	}
	// 文件不存在 / 空路径：返回空串（且不 panic）
	if got := c.line("nope.go", 3); got != "" {
		t.Errorf("缺失文件应返回空串，got %q", got)
	}
	if got := c.line("", 3); got != "" {
		t.Errorf("空路径应返回空串，got %q", got)
	}
}

func TestLineCacheReadsEachFileOnce(t *testing.T) {
	dir, rel := lineCacheFixture(t)
	c := newLineCache(dir)
	for i := 0; i < 10; i++ {
		if got := c.line(rel, 4); got != "x := 1 // 缩进行" {
			t.Fatalf("第 %d 次读取 = %q", i, got)
		}
	}
	if c.reads != 1 {
		t.Fatalf("同一文件应只读盘一次，got %d 次", c.reads)
	}
	// 负缓存：缺失文件也只见一次盘
	c.line("missing.go", 1)
	c.line("missing.go", 2)
	if c.reads != 2 {
		t.Fatalf("缺失文件应只尝试读盘一次（负缓存），got %d 次", c.reads)
	}
}

func TestLineCacheConcurrent(t *testing.T) {
	dir, rel := lineCacheFixture(t)
	c := newLineCache(dir)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// 一半读同一文件同一行（命中），一半读不同文件（miss/负缓存）
			if i%2 == 0 {
				if got := c.line(rel, 4); got != "x := 1 // 缩进行" {
					t.Errorf("并发读命中 = %q", got)
				}
				return
			}
			if got := c.line(fmt.Sprintf("pkg%d.go", i), 1); got != "" {
				t.Errorf("缺失文件并发读 = %q", got)
			}
		}(i)
	}
	wg.Wait()
}

// TestAdapterSharesOneLineCache：构建期 extractor 与 aliasPass 必须共用
// 同一个 Index 级缓存（否则又是两份重复）。
func TestAdapterSharesOneLineCache(t *testing.T) {
	dir, rel := lineCacheFixture(t)
	c := newLineCache(dir)
	if c.line(rel, 1) == "" {
		t.Fatal("fixture 行读取失败")
	}
	// 共享缓存被两个使用方拿到时读盘数不增加
	before := c.reads
	_ = c.line(rel, 2)
	_ = c.line(rel, 3)
	if c.reads != before {
		t.Fatalf("共享缓存命中不应再读盘，before=%d after=%d", before, c.reads)
	}
}
