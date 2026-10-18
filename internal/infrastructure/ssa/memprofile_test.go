package ssa

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestPeakSamplerIsolatedAndStats：堆采样——GC 后峰值落盘、阶段快照与
// 全程累计分开保存、状态按构建隔离（todo2-11）。
func TestPeakSamplerIsolatedAndStats(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "peak.prof")
	t.Setenv("CODEINTEL_MEM_PROFILE", path)
	resetPeakSampler()

	// 大块存活分配：保证 post-GC live 明显 > 0
	big := make([]byte, 16<<20)
	for i := range big {
		big[i] = 1
	}

	dumpPeakHeapProfile("stage1", 1<<30) // pre-GC 大值 → 触发 GC + 刷新峰值
	if peakState.peakLive == 0 {
		t.Fatal("post-GC 峰值应更新")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("峰值 profile 未生成: %v", err)
	}
	firstPeak := peakState.peakLive
	firstDumps := peakState.dumps

	// 较小 pre-GC：不应刷新峰值/重复落盘
	dumpPeakHeapProfile("stage2", 1)
	if peakState.peakLive != firstPeak || peakState.dumps != firstDumps {
		t.Errorf("较小 pre-GC 不应刷新峰值（%d→%d, dumps %d→%d）",
			firstPeak, peakState.peakLive, firstDumps, peakState.dumps)
	}

	finishPeakSampler()
	data, err := os.ReadFile(path + ".stats.json")
	if err != nil {
		t.Fatalf("统计文件未生成: %v", err)
	}
	var st peakStats
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatalf("stats JSON: %v", err)
	}
	if st.PeakLiveBytes == 0 {
		t.Error("peak_live_bytes 应 > 0")
	}
	if st.TotalAllocBytes == 0 || st.Mallocs == 0 {
		t.Errorf("全程累计分配应记录（total=%d mallocs=%d）", st.TotalAllocBytes, st.Mallocs)
	}
	if len(st.StageSnapshots) != 2 || st.StageSnapshots[0].Stage != "stage1" || st.StageSnapshots[1].Stage != "stage2" {
		t.Errorf("stage_snapshots = %+v, want stage1/stage2", st.StageSnapshots)
	}

	// 构建隔离：reset 清空
	resetPeakSampler()
	if peakState.peakLive != 0 || peakState.dumps != 0 || len(peakState.stages) != 0 {
		t.Errorf("reset 应清空采样状态：live=%d dumps=%d stages=%d",
			peakState.peakLive, peakState.dumps, len(peakState.stages))
	}
	runtime.KeepAlive(big)
}

// TestPeakSamplerDisabled：未设置 CODEINTEL_MEM_PROFILE 时不落盘、不做 GC
// 检查（只在阶段快照里记录 pre-GC 值）。
func TestPeakSamplerDisabled(t *testing.T) {
	t.Setenv("CODEINTEL_MEM_PROFILE", "")
	resetPeakSampler()
	dumpPeakHeapProfile("stage1", 123)
	if peakState.peakLive != 0 || peakState.dumps != 0 {
		t.Errorf("未启用时不应落盘/刷新峰值：live=%d dumps=%d", peakState.peakLive, peakState.dumps)
	}
	if len(peakState.stages) != 1 {
		t.Errorf("阶段快照 = %d, want 1", len(peakState.stages))
	}
	finishPeakSampler()
}
