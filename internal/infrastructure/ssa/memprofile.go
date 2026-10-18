package ssa

// Q247 §2.3 / todo2-11：构建期堆采样（真实峰值 + 阶段快照 + 全程累计）。
//
// 为什么不是退出时 dump：进程退出时的 live 堆是"构建产物已释放"之后的
// 状态，看不到峰值构成（go2o 实测峰值出现在 emitFunction 循环中段）。
//
// 统一 GC 时点（todo2-11）：
//   - **真实峰值 = GC 后的 HeapAlloc**（live），且只在超过当前峰值时落盘
//     profile（同一文件反复覆盖——最后一次即峰值时刻的 live 构成）
//   - 阶段边界传入的 HeapAlloc（**pre-GC，含未回收垃圾**）只作廉价预筛 +
//     "阶段快照"趋势记录；不与 post-GC 峰值混用比较
//   - 落盘前强制 GC，profile 不含垃圾；峰值单调递增 → 较早快照不会
//     遗漏后续更高的存活堆
//   - 构建结束**另行**记录全程累计分配（TotalAlloc/Mallocs/NumGC/
//     PauseTotalNs）到 <profile>.stats.json——累计值不是峰值，不用于
//     与峰值比较
//   - 采样状态**按构建隔离**：Index 开始 reset，结束 finish
//
// 用法：CODEINTEL_MEM_PROFILE=/tmp/peak.prof codeintel init --repo X
//       go tool pprof -top -inuse_space <codeintel 二进制> /tmp/peak.prof

import (
	"encoding/json"
	"os"
	"runtime"
	"runtime/pprof"
	"sync"

	"go.uber.org/zap"
)

// stageHeapSnap 阶段边界快照（pre-GC HeapAlloc——含垃圾，仅趋势）。
type stageHeapSnap struct {
	Stage string `json:"stage"`
	PreGC uint64 `json:"pre_gc_heap_bytes"`
}

// peakSampler 构建期采样状态（按构建隔离）。
type peakSampler struct {
	mu       sync.Mutex
	path     string
	peakLive uint64 // post-GC HeapAlloc 峰值
	dumps    int
	stages   []stageHeapSnap
}

var peakState = &peakSampler{}

// peakStats 构建结束落盘的统计（峰值与累计分开、口径明确）。
type peakStats struct {
	PeakLiveBytes   uint64          `json:"peak_live_bytes"` // GC 后峰值（与 profile 同时刻）
	PeakLiveMB      int64           `json:"peak_live_mb"`
	TotalAllocBytes uint64          `json:"total_alloc_bytes"` // 全程累计分配（不是峰值）
	Mallocs         uint64          `json:"mallocs"`
	NumGC           uint32          `json:"num_gc"`
	PauseTotalNs    uint64          `json:"pause_total_ns"`
	ProfileDumps    int             `json:"profile_dumps"`
	StageSnapshots  []stageHeapSnap `json:"stage_snapshots"` // pre-GC，仅趋势
}

// resetPeakSampler 构建开始时重置采样状态（不跨构建复用）。
func resetPeakSampler() {
	peakState.mu.Lock()
	defer peakState.mu.Unlock()
	peakState.path = os.Getenv("CODEINTEL_MEM_PROFILE")
	peakState.peakLive = 0
	peakState.dumps = 0
	peakState.stages = nil
}

// dumpPeakHeapProfile 阶段边界调用：preGC 为阶段快照（含垃圾，仅趋势）。
// 只有 GC 后的 live HeapAlloc 刷新峰值时才写 profile。
func dumpPeakHeapProfile(stage string, preGC uint64) {
	peakState.mu.Lock()
	defer peakState.mu.Unlock()
	peakState.stages = append(peakState.stages, stageHeapSnap{Stage: stage, PreGC: preGC})
	if peakState.path == "" {
		return
	}
	// 廉价预筛：pre-GC 都未超过当前 post-GC 峰值 → 不可能刷新
	if preGC <= peakState.peakLive {
		return
	}
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	if ms.HeapAlloc <= peakState.peakLive {
		return // pre-GC 峰值来自垃圾；post-GC 未刷新
	}
	peakState.peakLive = ms.HeapAlloc
	f, err := os.Create(peakState.path)
	if err != nil {
		return
	}
	err = pprof.Lookup("heap").WriteTo(f, 0)
	_ = f.Close()
	if err != nil {
		return
	}
	peakState.dumps++
	zap.L().Info("peak heap profile saved",
		zap.String("file", peakState.path), zap.Int64("heap_mb", int64(ms.HeapAlloc>>20)))
}

// finishPeakSampler 构建结束：写 <profile>.stats.json（峰值 + 全程累计，
// 口径分开）。未启用 profile 时空转。
func finishPeakSampler() {
	peakState.mu.Lock()
	path := peakState.path
	stats := peakStats{
		PeakLiveBytes:  peakState.peakLive,
		PeakLiveMB:     int64(peakState.peakLive >> 20),
		ProfileDumps:   peakState.dumps,
		StageSnapshots: append([]stageHeapSnap(nil), peakState.stages...),
	}
	peakState.mu.Unlock()
	if path == "" {
		return
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	stats.TotalAllocBytes = ms.TotalAlloc
	stats.Mallocs = ms.Mallocs
	stats.NumGC = ms.NumGC
	stats.PauseTotalNs = ms.PauseTotalNs
	data, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		return
	}
	if err := os.WriteFile(path+".stats.json", data, 0o644); err != nil {
		zap.L().Warn("write peak stats failed", zap.Error(err))
		return
	}
	zap.L().Info("peak heap stats saved",
		zap.String("file", path+".stats.json"),
		zap.Int64("peak_live_mb", stats.PeakLiveMB),
		zap.Uint64("total_alloc_mb", stats.TotalAllocBytes>>20),
		zap.Uint32("num_gc", stats.NumGC))
}
