package sqlite

// P2 淘汰策略对照：整表清空 vs 两代分段轮换（同容量）。
//
// 真实端点序列（大仓 13.1% 命中率那批）不在本机，这里用**同构的幂律
// 访问序列**（小索引高频 + 周期性突发）做局部对照，只看相对收益；端到端
// 大仓命中率仍需在大仓副本上实测（见 docs/codeintel-gc-investigation.md）。

import (
	"math/rand"
	"strconv"
	"testing"
)

// skewedTrace 生成幂律访问序列（pool 个不同键，n 次访问）。
func skewedTrace(n, pool int, seed int64) []string {
	r := rand.New(rand.NewSource(seed))
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		idx := int(float64(pool) * r.Float64() * r.Float64() * r.Float64())
		out = append(out, "id"+strconv.Itoa(idx))
	}
	return out
}

// simulateClear 整表清空策略的命中率。
func simulateClear(capacity int, trace []string) float64 {
	m := map[string]bool{}
	hits := 0
	for _, id := range trace {
		if m[id] {
			hits++
			continue
		}
		if len(m) >= capacity {
			m = map[string]bool{}
		}
		m[id] = true
	}
	return float64(hits) / float64(len(trace))
}

// TestEvictionHitRateSegmentedBeatsClear：同容量下两代分段轮换的命中率
// 不低于整表清空（幂律序列的回归护栏；收益幅度记录在调查文档）。
func TestEvictionHitRateSegmentedBeatsClear(t *testing.T) {
	const capacity = 32768
	for _, seed := range []int64{1, 2, 3} {
		trace := skewedTrace(200000, 1_000_000, seed)
		clear := simulateClear(capacity, trace)
		seg := simulateSegmented(trace, capacity)
		t.Logf("seed=%d whole-clear=%.4f segmented=%.4f", seed, clear, seg)
		if seg < clear {
			t.Errorf("seed=%d segmented %.4f < clear %.4f", seed, seg, clear)
		}
	}
}

// simulateSegmented 复刻生产实现（buildWriteCache：cur 达 half 时 prev←cur、
// cur 清空；prev 命中且 cur 有空间时晋升）。
func simulateSegmented(trace []string, capacity int) float64 {
	cur, prev := map[string]bool{}, map[string]bool{}
	half := capacity / 2
	hits := 0
	for _, id := range trace {
		if cur[id] {
			hits++
			continue
		}
		if prev[id] {
			hits++
			if len(cur) < half {
				cur[id] = true
			}
			continue
		}
		if len(cur) >= half {
			prev = cur
			cur = map[string]bool{}
		}
		cur[id] = true
	}
	return float64(hits) / float64(len(trace))
}
