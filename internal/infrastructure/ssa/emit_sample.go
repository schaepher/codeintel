package ssa

// emitFunction 容器采样诊断（CODEINTEL_EMIT_SAMPLE=1）：大仓 GC 扫描
// 压力归因——在**函数处理结束后**抽样 fieldExtractor 的 map、嵌套 slots/
// returns、参数调用者切片（paramCallerCache），估算累计函数数/map 数/
// entry 数、最大采样提取器条目总数及堆/GC/耗时变化。
//
// 限制：抽样结果**不是** map 实际桶容量或 GC 扫描字节数；map 数按固定
// 系数外推，没有区分惰性分配与嵌套 map，累计条目也不是同时存活的容器
// 数或 GC 扫描对象数。大仓结论需在足够样本数下结合 CPU/heap profile 与
// GODEBUG=gctrace=1 判断。

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"
)

// emitSampleEnabled 包级一次读取（构建过程不重复 getenv）。
var emitSampleEnabled = os.Getenv("CODEINTEL_EMIT_SAMPLE") == "1"

// emitSampleState 采样累计（并发构建下加锁——emitFunctionFields 按包并发）。
var emitSampleState struct {
	sync.Mutex
	functions  int
	maps       int
	entries    int
	maxEntries int
	start      time.Time
}

// sampleEmitContainers 每 10 个函数抽样一次（函数处理结束后由 defer 调用）：
// 累计函数数/map 数/entry 估算、最大采样提取器条目总数 + 堆/GC/耗时。
func sampleEmitContainers(ext *fieldExtractor) {
	if !emitSampleEnabled || ext == nil {
		return
	}
	emitSampleState.Lock()
	defer emitSampleState.Unlock()
	if emitSampleState.start.IsZero() {
		emitSampleState.start = time.Now()
	}
	emitSampleState.functions++
	if emitSampleState.functions%10 != 0 {
		return
	}
	mapCount, entries := extractorStats(ext)
	emitSampleState.maps += mapCount
	emitSampleState.entries += entries
	if entries > emitSampleState.maxEntries {
		emitSampleState.maxEntries = entries
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	fmt.Fprintf(os.Stderr,
		"[emit-sample] functions=%d maps=%d entries=%d max_extractor_entries=%d heap=%dMB num_gc=%d elapsed=%s\n",
		emitSampleState.functions, emitSampleState.maps, emitSampleState.entries, emitSampleState.maxEntries,
		ms.HeapAlloc>>20, ms.NumGC, time.Since(emitSampleState.start).Round(time.Millisecond))
}

// extractorStats 单个提取器的容器统计：顶层 map 数/entry 数 + 嵌套
// slots（map[string]bool）/returns（[][]ssa.Value）/参数调用者切片
// （paramCalls.callers）条目；返回 (map 数, 总条目数)。
func extractorStats(ext *fieldExtractor) (maps, entries int) {
	top := []int{
		len(ext.fields), len(ext.reads), len(ext.indexes), len(ext.indexReads),
		len(ext.values), len(ext.funcIDs), len(ext.extSummaries),
		len(ext.chainTables), len(ext.tableNames), len(ext.slotsFor), len(ext.rets),
		len(ext.paramCallerCache),
	}
	for _, n := range top {
		if n > 0 {
			maps++
		}
		entries += n
	}
	// 嵌套集合
	for _, m := range ext.slotsFor {
		entries += len(m)
	}
	for _, rs := range ext.rets {
		entries += len(rs)
	}
	for _, pc := range ext.paramCallerCache {
		if pc != nil {
			entries += len(pc.callers)
		}
	}
	return maps, entries
}
