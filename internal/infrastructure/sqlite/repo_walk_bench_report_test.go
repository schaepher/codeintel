package sqlite

// todo22-14.4-B：资源基准的**人读汇总**（summary.json / summary.md 的写出与
// markdown 渲染）。聚合逻辑在 repo_walk_bench_summary_test.go。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// writeWalkBenchSummary 写 summary.json + summary.md（人读汇总表）。
func writeWalkBenchSummary(t *testing.T, dir string, sum *benchSummary) {
	t.Helper()
	jsonPath := filepath.Join(dir, "summary.json")
	data, err := json.MarshalIndent(sum, "", "  ")
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}
	if err := os.WriteFile(jsonPath, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write summary.json: %v", err)
	}
	md := renderWalkBenchSummary(sum)
	mdPath := filepath.Join(dir, "summary.md")
	if err := os.WriteFile(mdPath, []byte(md), 0o644); err != nil {
		t.Fatalf("write summary.md: %v", err)
	}
	t.Logf("\n%s", md)
}

func renderWalkBenchSummary(sum *benchSummary) string {
	var b strings.Builder
	b.WriteString("# 递归查询资源基准汇总\n\n")
	m := sum.Meta
	b.WriteString(fmt.Sprintf("- 时间: %s\n- Go: %s / %s-%s / GOMAXPROCS=%d (NumCPU=%d)\n",
		m.StartedAt, m.GoVersion, m.GOOS, m.GOARCH, m.GOMAXPROCS, m.NumCPU))
	b.WriteString(fmt.Sprintf("- 驱动: modernc.org/sqlite %s / SQLite %s / temp_store=%d / cache_size=%d 页\n",
		m.DriverVersion, m.SQLiteVersion, m.TempStore, m.CacheSize))
	mem := "unavailable"
	if m.MemTotal.OK {
		mem = fmt.Sprintf("%d MiB", m.MemTotal.Value/(1<<20))
	}
	b.WriteString(fmt.Sprintf("- 机器内存: %s / 每组样本 %d 次 / 共 %d 样本\n", mem, m.Reps, m.SampleCount))
	b.WriteString(fmt.Sprintf("- 峰值测量: %s\n- 临时文件采样: %s\n", m.TimeTool, m.TempSampling))
	for _, fx := range m.Builds {
		b.WriteString(fmt.Sprintf("- 夹具 N=%d: nodes=%d edges=%d 建图 %.1fms\n",
			fx.N, fx.Nodes, fx.Edges, fx.BuildMS))
	}
	b.WriteString("\n| N | depth | 期望边 | 摘要(前12) | recursive 中位ms | materialized 中位ms | direct 中位ms | 物化/原递归耗时 | 最大RSS rec/mat (MiB) | RSS比 | 临时峰值 rec/mat (B) |\n")
	b.WriteString("| --- | --- | --- | --- | ---: | ---: | ---: | ---: | --- | --- | --- |\n")
	for _, g := range sum.Groups {
		digest := g.ResultDigest
		if len(digest) > 12 {
			digest = digest[:12]
		}
		ratio := "n/a"
		if g.TimeRatio != 0 {
			ratio = fmt.Sprintf("%.2fx", g.TimeRatio)
		}
		rss := fmt.Sprintf("%s/%s", benchMiB(g.MaxRSSBytes[benchVariantRecursive]),
			benchMiB(g.MaxRSSBytes[benchVariantMaterialized]))
		rssRatio := "n/a"
		if g.RSSRatio != 0 {
			rssRatio = fmt.Sprintf("%.2fx", g.RSSRatio)
		}
		direct := "-"
		if v, ok := g.MedianQueryMS[benchVariantDirectOneHop]; ok {
			direct = fmt.Sprintf("%.2f", v)
		}
		b.WriteString(fmt.Sprintf("| %d | %d | %d | %s | %.2f | %.2f | %s | %s | %s | %s | %s/%s |\n",
			g.N, g.Depth, g.ExpectedEdges, digest,
			g.MedianQueryMS[benchVariantRecursive],
			g.MedianQueryMS[benchVariantMaterialized], direct, ratio, rss, rssRatio,
			benchBytes(g.TempPeakBytes[benchVariantRecursive]),
			benchBytes(g.TempPeakBytes[benchVariantMaterialized])))
	}
	if c := m.CancelContract; c != nil {
		b.WriteString(fmt.Sprintf("\n取消契约自检: depth=%d 预算 %.0fms → elapsed=%.1fms, DeadlineExceeded=%t, 复用后一跳边=%d\n",
			c.Depth, c.BudgetMS, c.ElapsedMS, c.DeadlineExceeded, c.ReuseAfterCancelEdges))
	}
	b.WriteString("\n## 测量边界\n\n")
	b.WriteString("- " + m.ColdWarmNote + "\n")
	b.WriteString("- " + m.BoundaryNote + "\n")
	b.WriteString("- 临时文件峰值 0 表示“采样覆盖全查询生命周期且始终未出现落盘临时文件”（SQLite 页缓存足够），不是采集失败。\n")
	b.WriteString("- 本夹具是 SQL 资源机制夹具（N=256/4096，3N 条边），不是完整源码 init，也不代表真实大仓（如百万节点）的绝对值。\n")
	return b.String()
}

func benchMiB(m benchMeasure) string {
	if !m.OK {
		return "unavailable"
	}
	return fmt.Sprintf("%.1f", float64(m.Value)/(1<<20))
}

func benchBytes(m benchMeasure) string {
	if !m.OK {
		return "unavailable"
	}
	return strconv.FormatInt(m.Value, 10)
}
