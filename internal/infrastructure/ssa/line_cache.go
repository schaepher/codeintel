package ssa

// Q248：Index 级共享源码行缓存。
//
// 改造前每个函数各建一个 `fieldExtractor`（内含 `lines map[string][]string`），
// `sourceLine` 第一次用它就把**整份源文件** os.ReadFile + strings.Split
// 一遍；aliasPass 另有一份同样的缓存。go2o 实测（峰值 heap profile）：
// sourceLine 占全部分配 25%（cum 1084MB）、峰值时刻 live 66%（412MB），
// 而 go2o 源码总共才 41MB。这是纯 churn：读同一文件的 1.2 万份副本。
//
// 现改为 Index 级一份共享缓存（extractor 与 aliasPass 都用它）：
//   - 内存：常驻 = 被访问文件的源码行（go2o ≈ 40-70MB），远低于原先的 churn
//   - 语义：与旧实现逐字一致（同一份 `strings.Split` + `TrimSpace(lines[n-1])`，
//     越界/读失败返回空串并负缓存），由 line_cache_test.go 锁定
//   - 并发（todo8-12）：同一文件的**并发首次**请求共享一次读取与分行
//     （entry.ready 等待）；不同文件仍并行读取；读盘不在全局锁内执行。
//     错误负缓存/越界/TrimSpace 语义不变。

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// lineEntry 单文件加载状态：ready 关闭即内容可用（并发首次共享一次读取）。
type lineEntry struct {
	ready chan struct{}
	lines []string
}

// lineCache 源码行缓存（Index/构建级）。
type lineCache struct {
	repoPath string
	// readFile 读盘实现（默认 os.ReadFile；测试注入以控制并发/失败）
	readFile func(string) ([]byte, error)
	mu       sync.Mutex
	files    map[string]*lineEntry
	// reads 实际读盘次数（负缓存也计一次）——测试/诊断用
	reads int
}

func newLineCache(repoPath string) *lineCache {
	return &lineCache{repoPath: repoPath, readFile: os.ReadFile, files: map[string]*lineEntry{}}
}

// entry 取（或创建）文件条目；创建者负责读盘并 close(ready)。
func (c *lineCache) entry(filePath string) *lineEntry {
	c.mu.Lock()
	if e, ok := c.files[filePath]; ok {
		c.mu.Unlock()
		return e
	}
	e := &lineEntry{ready: make(chan struct{})}
	c.files[filePath] = e
	c.mu.Unlock()
	// 唯一读盘者；读盘在锁外（不同文件可并行）
	read := c.readFile
	if read == nil {
		read = os.ReadFile
	}
	data, err := read(filepath.Join(c.repoPath, filepath.FromSlash(filePath)))
	if err != nil {
		data = nil // 负缓存（读失败不反复读盘）
	}
	lines := strings.Split(string(data), "\n")
	c.mu.Lock()
	c.reads++
	e.lines = lines
	close(e.ready)
	c.mu.Unlock()
	return e
}

// line 返回 filePath 第 line 行的源码（去首尾空白）；越界/文件不存在返回 ""。
func (c *lineCache) line(filePath string, line int) string {
	if c == nil || filePath == "" || line < 1 {
		return ""
	}
	e := c.entry(filePath)
	<-e.ready // 同一文件并发首次共享同一次读取（close 后发 happens-before）
	if line > len(e.lines) {
		return ""
	}
	return strings.TrimSpace(e.lines[line-1])
}
