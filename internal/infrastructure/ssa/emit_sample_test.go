package ssa

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/tools/go/ssa"

	"github.com/schaepher/codeintel/internal/domain"
)

// captureStderr 捕获 os.Stderr 输出（采样诊断写 stderr）。
func captureStderr(f func()) string {
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		f()
		return ""
	}
	os.Stderr = w
	f()
	w.Close()
	os.Stderr = old
	b, _ := io.ReadAll(r)
	return string(b)
}

// resetEmitSampleState 清零采样累计（测试用）。
func resetEmitSampleState() {
	emitSampleState.Lock()
	emitSampleState.functions = 0
	emitSampleState.maps = 0
	emitSampleState.entries = 0
	emitSampleState.maxEntries = 0
	emitSampleState.start = time.Time{}
	emitSampleState.Unlock()
}

// TestExtractorStatsNested：容器采样统计计入嵌套 slots / returns / 参数
// 调用者切片（顶层 map 数与总条目数正确）。
func TestExtractorStatsNested(t *testing.T) {
	ext := &fieldExtractor{
		slotsFor:         map[domain.CanonicalID]map[string]bool{"f": {"a": true, "b": true}},
		rets:             map[*ssa.Function][][]ssa.Value{nil: {{}, {}}},
		paramCallerCache: map[*ssa.Function]*paramCalls{nil: {callers: [][]ssa.Value{{}, {}, {}}}},
	}
	maps, entries := extractorStats(ext)
	// 顶层：slotsFor(1) + rets(1) + paramCallerCache(1) = 3 个非空 map，
	// 顶层条目 3；嵌套：slots 值 2 + rets 值 2 + callers 3 = 7；合计 10。
	if maps != 3 {
		t.Errorf("maps = %d; want 3", maps)
	}
	if entries != 10 {
		t.Errorf("entries = %d; want 10", entries)
	}
}

// TestEmitSampleSwitch：CODEINTEL_EMIT_SAMPLE 关闭时不输出；开启后每 10 个
// 函数输出一次（含累计 map/entry、最大提取器条目、heap/GC、elapsed）。
func TestEmitSampleSwitch(t *testing.T) {
	oldEnabled := emitSampleEnabled
	defer func() {
		emitSampleEnabled = oldEnabled
		resetEmitSampleState()
	}()
	ext := &fieldExtractor{slotsFor: map[domain.CanonicalID]map[string]bool{"f": {"a": true}}}

	emitSampleEnabled = false
	resetEmitSampleState()
	out := captureStderr(func() {
		for i := 0; i < 10; i++ {
			sampleEmitContainers(ext)
		}
	})
	if out != "" {
		t.Errorf("关闭采样时不应输出：%q", out)
	}

	emitSampleEnabled = true
	resetEmitSampleState()
	out = captureStderr(func() {
		for i := 0; i < 10; i++ {
			sampleEmitContainers(ext)
		}
	})
	if !strings.Contains(out, "[emit-sample]") {
		t.Fatalf("开启采样应输出：%q", out)
	}
	for _, want := range []string{"functions=10", "maps=", "entries=", "max_extractor_entries=", "heap=", "num_gc=", "elapsed="} {
		if !strings.Contains(out, want) {
			t.Errorf("采样输出缺 %q：%q", want, out)
		}
	}
}
