package ssa

// emitFunction 容器采样诊断（CODEINTEL_EMIT_SAMPLE=1）：大仓 GC 扫描
// 压力归因——抽样 fieldExtractor 的 map 与嵌套切片规模，估算函数数、
// map/entry 数、最大抽样 entry 数及堆/GC 变化。
//
// 限制：抽样结果**不是** map 实际桶容量或 GC 扫描字节数；大仓结论需在
// 足够样本数下结合 CPU/heap profile 与 GODEBUG=gctrace=1 判断。

import (
	"fmt"
	"os"
	"runtime"
	"sync"
)

// emitSampleEnabled 包级一次读取（构建过程不重复 getenv）。
var emitSampleEnabled = os.Getenv("CODEINTEL_EMIT_SAMPLE") == "1"

// emitSampleState 采样累计（并发构建下加锁——emitFunctionFields 按包并发）。
var emitSampleState struct {
	sync.Mutex
	functions int
}

// sampleEmitContainers 每 10 个函数抽样一次容器规模并写 stderr：
// 估算函数数、map/entry 数、最大单容器 entry 数 + 堆/GC 快照。
func sampleEmitContainers(ext *fieldExtractor) {
	if !emitSampleEnabled || ext == nil {
		return
	}
	emitSampleState.Lock()
	defer emitSampleState.Unlock()
	emitSampleState.functions++
	if emitSampleState.functions%10 != 0 {
		return
	}
	maps := []int{
		len(ext.fields), len(ext.reads), len(ext.indexes), len(ext.indexReads),
		len(ext.values), len(ext.funcIDs), len(ext.extSummaries),
		len(ext.chainTables), len(ext.tableNames), len(ext.slotsFor), len(ext.rets),
	}
	mapCount, entries, maxEntries := 0, 0, 0
	for _, n := range maps {
		if n > 0 {
			mapCount++
		}
		entries += n
		if n > maxEntries {
			maxEntries = n
		}
	}
	// 嵌套切片/集合（slotsFor 的 map[string]bool、rets 的 [][]ssa.Value）
	for _, m := range ext.slotsFor {
		entries += len(m)
		if len(m) > maxEntries {
			maxEntries = len(m)
		}
	}
	for _, rs := range ext.rets {
		entries += len(rs)
		if len(rs) > maxEntries {
			maxEntries = len(rs)
		}
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	fmt.Fprintf(os.Stderr,
		"[emit-sample] functions=%d maps=%d entries=%d max_entries=%d heap=%dMB num_gc=%d\n",
		emitSampleState.functions, mapCount, entries, maxEntries, ms.HeapAlloc>>20, ms.NumGC)
}
