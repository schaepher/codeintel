package orchestrator

// P1-13：flush（写库批次）延迟分布观测。
//
// 背景：写库是单写者串行路径（SQLite 单连接），"flush 慢不慢"此前只能看
// 总耗时与 >100ms 的单批慢日志（orch_run.flush），没有分位数——无法区分
// "少数长尾批次"与"每批都慢"，也就无法判断"要不要把写库流水线解耦"
// （Q254 §104 已实测否决两阶段导入，理由是 flush 只占 5–12%）。这里补
// p50/p95/max 的固定口径（最近秩法），构建结束写一行日志。
//
// 口径：只统计实际执行 SaveBatchStats 的批次（空批次直接返回、不计入）；
// 单写者串行 → 无锁追加；分位数用最近秩法（nearest-rank），样本量小时
// 只作趋势参考（不宣称跨机可比）。

import (
	"sort"
	"time"
)

// flushLatency p50/p95/max（最近秩法；空样本返回零值）。
func flushLatency(ds []time.Duration) (p50, p95, max time.Duration) {
	if len(ds) == 0 {
		return 0, 0, 0
	}
	sorted := append([]time.Duration(nil), ds...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	rank := func(p float64) time.Duration {
		// 最近秩：rank = ceil(p * n)，1-based；n 足够小也不越界
		idx := int(p*float64(len(sorted)) + 0.999999)
		if idx < 1 {
			idx = 1
		}
		if idx > len(sorted) {
			idx = len(sorted)
		}
		return sorted[idx-1]
	}
	return rank(0.50), rank(0.95), sorted[len(sorted)-1]
}
