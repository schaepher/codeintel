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
//   - 并发：多 worker（包间并行）会同时读，用 RWMutex 保护；读盘在锁外
//     执行（重复读盘无害，避免持锁做 I/O）

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// lineCache 源码行缓存（Index/构建级）。
type lineCache struct {
	repoPath string
	mu       sync.RWMutex
	files    map[string][]string // 仓库相对路径（slash）→ 行数组；nil = 读盘失败（负缓存）
	// reads 实际读盘次数（负缓存也计一次）——测试/诊断用
	reads int
}

func newLineCache(repoPath string) *lineCache {
	return &lineCache{repoPath: repoPath, files: map[string][]string{}}
}

// line 返回 filePath 第 line 行的源码（去首尾空白）；越界/文件不存在返回 ""。
func (c *lineCache) line(filePath string, line int) string {
	if c == nil || filePath == "" || line < 1 {
		return ""
	}
	c.mu.RLock()
	lines, ok := c.files[filePath]
	c.mu.RUnlock()
	if !ok {
		data, err := os.ReadFile(filepath.Join(c.repoPath, filepath.FromSlash(filePath)))
		if err != nil {
			data = nil // 负缓存（读失败不反复读盘）
		}
		lines = strings.Split(string(data), "\n")
		c.mu.Lock()
		c.reads++
		c.files[filePath] = lines
		c.mu.Unlock()
	}
	if line > len(lines) {
		return ""
	}
	return strings.TrimSpace(lines[line-1])
}
