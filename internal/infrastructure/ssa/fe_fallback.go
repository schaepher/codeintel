package ssa

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// R100 待办9：失败明细跨函数聚合——S1 的 fallbackDetails 是每函数切片，
// 相同 函数:路径 多次失败重复打印（50 条全局限量下纯重复项挤掉有信息量
// 的条目）。聚合器按 函数:路径 去重，输出每路径一行 ×N 次数。
// 多 goroutine 并发（emitFunction 块池）——内部加锁。

const fallbackDetailLimit = 50

type fallbackAgg struct {
	mu    sync.Mutex
	seen  map[string]*fallbackEntry // key: 函数名\x00路径
	count int64
}

type fallbackEntry struct {
	firstLine int
	count     int
}

func newFallbackAgg() *fallbackAgg {
	return &fallbackAgg{seen: map[string]*fallbackEntry{}}
}

// add 记录一次失败（函数名: 字段路径 → 聚合计次 + 首行）。
func (a *fallbackAgg) add(fn, path string, line int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.count++
	e, ok := a.seen[fn+"\x00"+path]
	if !ok {
		e = &fallbackEntry{firstLine: line}
		a.seen[fn+"\x00"+path] = e
	}
	e.count++
}

// total 失败总数（汇总警告用——替代原 fallbackTotal 原子计数）。
func (a *fallbackAgg) total() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.count
}

// dump 唯一明细（排序保证确定性输出；每行 函数: 路径: 行 N（共 M 次））。
func (a *fallbackAgg) dump() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, 0, len(a.seen))
	for k, e := range a.seen {
		fn, path, _ := strings.Cut(k, "\x00")
		line := ""
		if e.firstLine > 0 {
			line = fmt.Sprintf(": 行 %d", e.firstLine)
		}
		out = append(out, fmt.Sprintf("%s: %s%s（共 %d 次）", fn, path, line, e.count))
	}
	sort.Strings(out)
	return out
}
