package orchestrator

import (
	"testing"
	"time"
)

func TestFlushLatencyNearestRank(t *testing.T) {
	ms := func(n int) time.Duration { return time.Duration(n) * time.Millisecond }
	if p50, p95, max := flushLatency(nil); p50 != 0 || p95 != 0 || max != 0 {
		t.Fatalf("empty: %v %v %v", p50, p95, max)
	}
	// 1..20ms（20 个样本）：p50 = 第 10 个（10ms）、p95 = 第 19 个（19ms）
	var ds []time.Duration
	for i := 20; i >= 1; i-- {
		ds = append(ds, ms(i))
	}
	p50, p95, max := flushLatency(ds)
	if p50 != ms(10) || p95 != ms(19) || max != ms(20) {
		t.Fatalf("p50=%v p95=%v max=%v, want 10/19/20ms", p50, p95, max)
	}
	// 单样本：三值相同
	if p50, p95, max := flushLatency([]time.Duration{ms(7)}); p50 != ms(7) || p95 != ms(7) || max != ms(7) {
		t.Fatalf("single: %v %v %v", p50, p95, max)
	}
	// 尾部重：19 个 1ms + 1 个 500ms → p95 落在 1ms 段（20 样本 nearest-rank
	// 第 19 名），max 抓到长尾
	ds = nil
	for i := 0; i < 19; i++ {
		ds = append(ds, ms(1))
	}
	ds = append(ds, ms(500))
	p50, p95, max = flushLatency(ds)
	if p50 != ms(1) || p95 != ms(1) || max != ms(500) {
		t.Fatalf("tail: p50=%v p95=%v max=%v", p50, p95, max)
	}
	// 输入切片不被修改
	if ds[0] != ms(1) || ds[len(ds)-1] != ms(500) {
		t.Fatalf("input mutated: %v", ds)
	}
}
