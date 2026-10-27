package sqlite

// todo22-14.4-B：资源基准的版本/机器信息与**汇总对照**（summary.json +
// summary.md）。汇总只做已测量指标的聚合（耗时中位数、最大 RSS、临时存储
// 峰值、物化/原递归比），未取得的指标保持 "unavailable"。

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

type benchBuildMeta struct {
	N       int     `json:"n"`
	Nodes   int     `json:"nodes"`
	Edges   int     `json:"edges"`
	BuildMS float64 `json:"build_ms"`
}

type benchMeta struct {
	StartedAt     string           `json:"started_at"`
	GoVersion     string           `json:"go_version"`
	GOOS          string           `json:"goos"`
	GOARCH        string           `json:"goarch"`
	GOMAXPROCS    int              `json:"gomaxprocs"`
	NumCPU        int              `json:"num_cpu"`
	DriverVersion string           `json:"driver_version"`
	SQLiteVersion string           `json:"sqlite_version"`
	TempStore     int              `json:"temp_store"`
	CacheSize     int              `json:"cache_size_pages"`
	MemTotal      benchMeasure     `json:"mem_total_bytes"`
	TimeTool      string           `json:"time_tool"`
	TempSampling  string           `json:"temp_sampling"`
	Reps          int              `json:"reps_per_group"`
	SampleCount   int              `json:"sample_count"`
	ColdWarmNote  string           `json:"cold_warm_note"`
	BoundaryNote  string           `json:"boundary_note"`
	Builds        []benchBuildMeta `json:"fixtures"`
	// CancelContract 取消契约（深递归物化 + 同一连接复用）的实测结果。
	CancelContract *benchCancelResult `json:"cancel_contract,omitempty"`
}

// benchCancelResult 取消契约自检（query 预算 25ms、depth=1e6）。
type benchCancelResult struct {
	Fixture               string  `json:"fixture"`
	Target                string  `json:"target"`
	Depth                 int     `json:"depth"`
	BudgetMS              float64 `json:"budget_ms"`
	ElapsedMS             float64 `json:"elapsed_ms"`
	DeadlineExceeded      bool    `json:"deadline_exceeded"`
	Error                 string  `json:"error,omitempty"`
	ReuseAfterCancelEdges int     `json:"reuse_after_cancel_edges"`
	ReuseError            string  `json:"reuse_error,omitempty"`
}

// benchGroupRow 一档/一深度的对照汇总。
type benchGroupRow struct {
	N             int                     `json:"n"`
	Depth         int                     `json:"depth"`
	ExpectedEdges int                     `json:"expected_edges"`
	ResultEdges   int                     `json:"result_edges"`
	ResultDigest  string                  `json:"result_digest"`
	Samples       int                     `json:"valid_samples"`
	MedianQueryMS map[string]float64      `json:"median_query_ms"`
	MaxRSSBytes   map[string]benchMeasure `json:"max_rss_bytes"`
	TempPeakBytes map[string]benchMeasure `json:"temp_peak_bytes"`
	// TimeRatio 物化/原递归的耗时中位数比（>1 = 物化更慢）。
	TimeRatio float64 `json:"materialized_vs_recursive_time_ratio"`
	// RSSRatio 物化/原递归的最大 RSS 比（峰值都拿到时才有值）。
	RSSRatio     float64 `json:"materialized_vs_recursive_rss_ratio"`
	RSSRatioNote string  `json:"rss_ratio_note,omitempty"`
}

type benchSummary struct {
	Meta   benchMeta       `json:"meta"`
	Groups []benchGroupRow `json:"groups"`
}

// collectBenchMeta 记录 Go/驱动/SQLite 版本、GOMAXPROCS、连接级 pragma
// （temp_store/cache_size）、机器内存与测量口径。
func collectBenchMeta(t *testing.T, reps int) benchMeta {
	t.Helper()
	meta := benchMeta{
		StartedAt:  time.Now().Format(time.RFC3339),
		GoVersion:  runtime.Version(),
		GOOS:       runtime.GOOS,
		GOARCH:     runtime.GOARCH,
		GOMAXPROCS: runtime.GOMAXPROCS(0),
		NumCPU:     runtime.NumCPU(),
		Reps:       reps,
		MemTotal:   benchMeasure{Note: "读取 /proc/meminfo MemTotal 失败"},
		TimeTool:   "unavailable（回退子进程自报 VmHWM）",
		TempSampling: fmt.Sprintf("/proc/<pid>/fd 采样，间隔 %.0fms；覆盖 SQLite 已 unlink 仍打开的临时文件",
			float64(benchTempInterval.Microseconds())/1000),
		ColdWarmNote: "cold_first_query = 该 (N) 库上本轮的第一次查询；其余 warm_repeat。" +
			"未 drop OS page cache（无权限），且夹具刚由 SaveBatchStats 写入——" +
			"故 cold 仅表示“该库首个查询进程”，不代表冷 OS 缓存；每样本都是新进程（SQLite 连接级缓存必冷）。",
		BoundaryNote: "query_ms 含 QueryContext + 全部 Rows.Next/Scan + rows.Close 之前；" +
			"rss_before_bytes 是查询前 RSS（不是查询增量）；max_rss_bytes 是进程峰值 RSS；" +
			"temp_peak_bytes 是查询全生命周期内 TMPDIR 下打开文件大小之和的峰值。" +
			"所有变体使用同一构建配置（同一测试二进制、同 DSN）。",
	}
	if v := os.Getenv(benchRepsEnv); v != "" {
		meta.Reps = reps
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range info.Deps {
			if dep.Path == "modernc.org/sqlite" {
				meta.DriverVersion = dep.Version
			}
		}
	}
	if meta.DriverVersion == "" {
		meta.DriverVersion = "unknown"
	}
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.HasPrefix(line, "MemTotal:") {
				continue
			}
			f := strings.Fields(line)
			if len(f) >= 2 {
				if kb, err := strconv.ParseInt(f[1], 10, 64); err == nil {
					meta.MemTotal = benchMeasure{Value: kb * 1024, OK: true}
				}
			}
			break
		}
	}
	if tool, ok := gnuTimeTool(); ok {
		meta.TimeTool = tool + " -v（Maximum resident set size）"
	}
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open probe db: %v", err)
	}
	meta.SQLiteVersion, meta.TempStore, meta.CacheSize = benchPragmas(t, db)
	db.Close()
	if meta.SQLiteVersion == "" {
		meta.SQLiteVersion = "unknown"
	}
	return meta
}

// summarizeWalkBench 按 (N, depth) 聚合：耗时中位数、最大 RSS、临时存储
// 峰值、物化/原递归的时间与 RSS 比。
func summarizeWalkBench(samples []*benchSample, meta benchMeta) *benchSummary {
	groups := map[[2]int][]*benchSample{}
	for _, s := range samples {
		if s.Error != "" || s.ExitCode != 0 {
			continue
		}
		k := [2]int{s.N, s.Depth}
		groups[k] = append(groups[k], s)
	}
	keys := make([][2]int, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	out := &benchSummary{Meta: meta}
	for _, k := range keys {
		list := groups[k]
		row := benchGroupRow{
			N: k[0], Depth: k[1], Samples: len(list),
			ExpectedEdges: benchExpectedEdges[k[0]][k[1]],
			MedianQueryMS: map[string]float64{},
			MaxRSSBytes:   map[string]benchMeasure{},
			TempPeakBytes: map[string]benchMeasure{},
		}
		byVariant := map[string][]*benchSample{}
		for _, s := range list {
			byVariant[s.Variant] = append(byVariant[s.Variant], s)
			if row.ResultEdges == 0 {
				row.ResultEdges, row.ResultDigest = s.ResultEdges, s.ResultDigest
			}
		}
		for _, v := range benchVariantsFor(k[1]) {
			vs := byVariant[v]
			if len(vs) == 0 {
				continue
			}
			times := make([]float64, 0, len(vs))
			rss := benchMeasure{Note: "no sample"}
			temp := benchMeasure{Note: "no sample"}
			for i, s := range vs {
				times = append(times, s.QueryMS)
				if s.MaxRSSBytes.OK && (!rss.OK || s.MaxRSSBytes.Value > rss.Value) {
					rss = s.MaxRSSBytes
				}
				if s.TempPeakBytes.OK && (!temp.OK || s.TempPeakBytes.Value > temp.Value) {
					temp = s.TempPeakBytes
				}
				_ = i
			}
			row.MedianQueryMS[v] = benchMedian(times)
			row.MaxRSSBytes[v] = rss
			row.TempPeakBytes[v] = temp
		}
		if r := row.MedianQueryMS[benchVariantRecursive]; r > 0 {
			row.TimeRatio = row.MedianQueryMS[benchVariantMaterialized] / r
		}
		rec, mat := row.MaxRSSBytes[benchVariantRecursive], row.MaxRSSBytes[benchVariantMaterialized]
		if rec.OK && mat.OK && rec.Value > 0 {
			row.RSSRatio = float64(mat.Value) / float64(rec.Value)
		} else {
			row.RSSRatioNote = "峰值 RSS 至少一侧 unavailable，无法给出比值"
		}
		out.Groups = append(out.Groups, row)
	}
	return out
}

func benchMedian(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	cp := append([]float64(nil), v...)
	sort.Float64s(cp)
	mid := len(cp) / 2
	if len(cp)%2 == 1 {
		return cp[mid]
	}
	return (cp[mid-1] + cp[mid]) / 2
}
