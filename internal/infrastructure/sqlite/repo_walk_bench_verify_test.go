package sqlite

// todo22-14.4-B：基准的**自动校验**——期望边数与跨变体摘要一致性，以及在
// 同一夹具上复验取消契约（深递归物化在 25ms 预算下真的被中断、同连接可复用）。

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
)

func verifyWalkBenchSamples(t *testing.T, samples []*benchSample, reps int) {
	t.Helper()
	type key struct {
		n, depth int
	}
	groups := map[key][]*benchSample{}
	for _, s := range samples {
		if s.Error != "" || s.ExitCode != 0 {
			continue
		}
		k := key{s.N, s.Depth}
		groups[k] = append(groups[k], s)
	}
	for _, n := range []int{256, 4096} {
		for _, depth := range benchDepths {
			variants := benchVariantsFor(depth)
			got := groups[key{n, depth}]
			// 样本齐全：每个变体 reps 条，且变体与期望集合一致。
			seen := map[string]int{}
			for _, s := range got {
				seen[s.Variant]++
			}
			for _, v := range variants {
				if seen[v] != reps {
					t.Errorf("N=%d depth=%d %s: 有效样本 %d 条，期望 %d", n, depth, v, seen[v], reps)
				}
			}
			want := benchExpectedEdges[n][depth]
			digests := map[string][]string{}
			for _, s := range got {
				if s.ResultEdges != want {
					t.Errorf("N=%d depth=%d %s sample=%d: result_edges=%d，期望 %d",
						n, depth, s.Variant, s.Sample, s.ResultEdges, want)
				}
				if s.ResultDigest == "" {
					t.Errorf("N=%d depth=%d %s sample=%d: 空摘要", n, depth, s.Variant, s.Sample)
				}
				digests[s.ResultDigest] = append(digests[s.ResultDigest], s.Variant)
			}
			if len(digests) > 1 {
				keys := make([]string, 0, len(digests))
				for d := range digests {
					keys = append(keys, d)
				}
				sort.Strings(keys)
				t.Errorf("N=%d depth=%d 结果摘要不一致（%d 种）: %v", n, depth, len(digests), keys)
			}
		}
	}
}

// verifyWalkBenchCancelContract 在夹具上校验取消契约（todo22-14.4-B 第 3 项）：
// 1e6 深度的物化递归在 25ms 预算下必须真的被 SQLite 中断（返回
// context.DeadlineExceeded、耗时 < 1s），随后**同一连接**立即可复用。
// 不用外层 goroutine 提前返回（那只能证明调用方放弃等待，不能证明 SQL
// 已停止）。
func verifyWalkBenchCancelContract(t *testing.T, fixtureDir string) *benchCancelResult {
	t.Helper()
	res := &benchCancelResult{
		Fixture: fixtureDir, Target: benchAnchor,
		Depth: 1000000, BudgetMS: 25,
	}
	db, err := Open(fixtureDir)
	if err != nil {
		t.Fatalf("取消契约：打开夹具库失败: %v", err)
	}
	defer db.Close()
	r := NewRepo(db)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = r.GetCalleesContext(ctx, domain.CanonicalID(benchAnchor), res.Depth, benchMinConfidence)
	res.ElapsedMS = benchMS(start)
	res.DeadlineExceeded = errors.Is(err, context.DeadlineExceeded)
	if err != nil {
		res.Error = err.Error()
	}
	if !res.DeadlineExceeded {
		t.Errorf("取消契约：25ms 预算应返回 context.DeadlineExceeded，got %v", err)
	}
	if res.ElapsedMS > 1000 {
		t.Errorf("取消契约：25ms 预算实际耗时 %.0fms（要求 <1s）", res.ElapsedMS)
	}
	facts, reuseErr := r.GetCallees(domain.CanonicalID(benchAnchor), 1, benchMinConfidence)
	res.ReuseAfterCancelEdges = len(facts)
	if reuseErr != nil {
		res.ReuseError = reuseErr.Error()
	}
	if reuseErr != nil || len(facts) != 3 {
		t.Errorf("取消契约：取消后同一连接应立即可复用（期望 3 条一跳边），got edges=%d err=%v",
			len(facts), reuseErr)
	}
	t.Logf("取消契约: elapsed=%.1fms deadline=%t reuse_edges=%d",
		res.ElapsedMS, res.DeadlineExceeded, res.ReuseAfterCancelEdges)
	return res
}
