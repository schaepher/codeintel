package ssa

// Q247 §2.3：构建期**峰值**堆 profile（第三轮 SSA 内存归因的前置工具）。
//
// 为什么不是退出时 dump：进程退出时的 live 堆是"构建产物已释放"之后的
// 状态，看不到峰值构成（go2o 实测峰值出现在 emitFunction 循环中段）。
// 这里在每个构建阶段边界检查 HeapAlloc，**只在刷新峰值时**落盘（同一
// 文件反复覆盖——最后一次即峰值时刻的 profile）。
//
// 用法：CODEINTEL_MEM_PROFILE=/tmp/peak.prof codeintel init --repo X
//       go tool pprof -top -inuse_space <codeintel 二进制> /tmp/peak.prof
// 落盘前强制一次 GC：否则 profile 里混入未回收垃圾，不是真实 live。

import (
	"os"
	"runtime"
	"runtime/pprof"
	"sync"

	"go.uber.org/zap"
)

var (
	peakProfileMu    sync.Mutex
	peakProfileBytes uint64 // 已落盘的最大 HeapAlloc
)

// dumpPeakHeapProfile 在 HeapAlloc 刷新峰值时写堆 profile（未设
// CODEINTEL_MEM_PROFILE 时空转）。heapAlloc 由调用方传入（同一时刻采样，
// 避免重复 ReadMemStats）。
func dumpPeakHeapProfile(heapAlloc uint64) {
	path := os.Getenv("CODEINTEL_MEM_PROFILE")
	if path == "" {
		return
	}
	peakProfileMu.Lock()
	defer peakProfileMu.Unlock()
	if heapAlloc <= peakProfileBytes {
		return
	}
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	runtime.GC() // 真实 live（否则含未回收垃圾）
	if err := pprof.Lookup("heap").WriteTo(f, 0); err != nil {
		return
	}
	peakProfileBytes = heapAlloc
	zap.L().Info("peak heap profile saved",
		zap.String("file", path), zap.Int64("heap_mb", int64(heapAlloc>>20)))
}
