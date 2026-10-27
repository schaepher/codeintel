package sqlite

// todo22-14.4-B：递归查询资源基准的**测量**基础设施（类型、峰值采集、
// 版本/机器信息）。编排在 repo_walk_bench_test.go、子进程在
// repo_walk_bench_child_test.go、汇总在 repo_walk_bench_summary_test.go。
//
// 测量口径（逐项写清，避免把不同量混为一谈）：
//   - rss_before_bytes：子进程**打开库之后、发起查询之前**的 VmRSS
//     （/proc/self/status）——它是"查询前 RSS"，不是"查询带来的增量"。
//   - max_rss_bytes：进程峰值 RSS。优先 `/usr/bin/time -v` 的
//     "Maximum resident set size"（父进程侧测量，覆盖进程全生命周期）；
//     该工具不可用（如 macOS 的 time 没有 -v）时回退子进程自报 VmHWM，
//     由 max_rss_source 标明来源；两者都拿不到 → "unavailable" + 原因。
//   - temp_peak_bytes：父进程按 5ms 采样 `/proc/<pid>/fd` 中**目标落在
//     子进程 TMPDIR 的全部 fd**（含 SQLite 创建后已 unlink、但仍打开的
//     临时文件——fd 目标形如 "... (deleted)" 仍能 stat 到大小），取全生命
//     周期最大值。采样间隔/次数随样本输出；/proc 不可用 → unavailable。

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// benchMeasure 峰值/资源的 JSON 表示：有效观测是数值（字节），采集失败是
// 字符串 "unavailable"——绝不用 0 冒充"没测"。
type benchMeasure struct {
	Value int64
	OK    bool
	Note  string
}

func (m benchMeasure) MarshalJSON() ([]byte, error) {
	if !m.OK {
		return []byte(`"unavailable"`), nil
	}
	return json.Marshal(m.Value)
}

// benchSample 一条样本（JSONL 一行）。字段与 todo22-14.4-B 输出契约一致，
// 额外带阶段耗时、采样信息与失败现场目录。
type benchSample struct {
	Order        int     `json:"order"`
	N            int     `json:"n"`
	Depth        int     `json:"depth"`
	Variant      string  `json:"variant"`
	Sample       int     `json:"sample"`
	SampleClass  string  `json:"sample_class"`
	Nodes        int     `json:"nodes"`
	Edges        int     `json:"edges"`
	ResultEdges  int     `json:"result_edges"`
	ResultDigest string  `json:"result_digest"`
	QueryMS      float64 `json:"query_ms"`
	ProcessWall  float64 `json:"process_wall_ms"`
	BuildMS      float64 `json:"build_ms,omitempty"`

	RSSBeforeBytes int64        `json:"rss_before_bytes"`
	MaxRSSBytes    benchMeasure `json:"max_rss_bytes"`
	MaxRSSSource   string       `json:"max_rss_source"`
	MaxRSSNote     string       `json:"max_rss_note,omitempty"`
	// MaxRSSVMHWMBytes 子进程自报的 VmHWM（交叉核对；与 gnu_time_v 同源
	// 内核计数，但统计范围略有差异：VmHWM 自进程启动起累计）。
	MaxRSSVMHWMBytes int64 `json:"max_rss_vmhwm_bytes,omitempty"`

	TempPeakBytes        benchMeasure     `json:"temp_peak_bytes"`
	TempPeakNote         string           `json:"temp_peak_note,omitempty"`
	TempSampleIntervalMS float64          `json:"temp_sample_interval_ms"`
	TempSamples          int              `json:"temp_samples"`
	TempMaxGapMS         float64          `json:"temp_sample_max_gap_ms"`
	TempPeakSource       string           `json:"temp_peak_source,omitempty"`
	TempEvents           []benchTempEvent `json:"temp_events,omitempty"`

	OpenMS   float64 `json:"open_ms"`
	CloseMS  float64 `json:"close_ms"`
	ExitCode int     `json:"exit_code"`

	SQLiteVersion string `json:"sqlite_version,omitempty"`
	TempStore     int    `json:"temp_store,omitempty"`
	CacheSize     int    `json:"cache_size_pages,omitempty"`
	Error         string `json:"error,omitempty"`
	ErrorDir      string `json:"error_dir,omitempty"`
}

// benchChildResult 子进程写出的测量结果（父进程读取后合并进 benchSample）。
type benchChildResult struct {
	N       int     `json:"n"`
	Depth   int     `json:"depth"`
	Variant string  `json:"variant"`
	Sample  int     `json:"sample"`
	OpenMS  float64 `json:"open_ms"`
	QueryMS float64 `json:"query_ms"`
	CloseMS float64 `json:"close_ms"`
	// QueryStartNS/QueryEndNS 查询边界（纳秒）：父进程据此判断临时文件
	// 采样点是否落在查询时间窗内。
	QueryStartNS int64  `json:"query_start_unix_nano"`
	QueryEndNS   int64  `json:"query_end_unix_nano"`
	ResultEdges  int    `json:"result_edges"`
	ResultDigest string `json:"result_digest"`
	RSSBefore    int64  `json:"rss_before_bytes"`
	VMHWMBytes   int64  `json:"max_rss_vmhwm_bytes"`
	SQLiteVer    string `json:"sqlite_version"`
	TempStore    int    `json:"temp_store"`
	CacheSize    int    `json:"cache_size_pages"`
	// todo23 14.4-B：子进程内查询生命周期的临时文件采样（短查询可靠覆盖）
	TempPeak       int64            `json:"temp_peak_bytes"`
	TempSamples    int              `json:"temp_samples"`
	TempMaxGapMS   float64          `json:"temp_max_gap_ms"`
	TempEvents     []benchTempEvent `json:"temp_events,omitempty"`
	TempPeakSource string           `json:"temp_peak_source,omitempty"`
	Error          string           `json:"error,omitempty"`
}

// procSampler 采样子进程 TMPDIR 下打开着的（含已 unlink 的）临时文件总量。
type procSampler struct {
	pid      int
	tmpDir   string
	interval time.Duration

	samples int
	// okSamples 成功完成的采样次数（进程退出的最后一次 ReadDir 失败
	// 不能把前面的有效观测作废）。
	okSamples int
	// sampleTimes 每次成功采样的起始纳秒时间戳——用于判断采样点是否真的
	// 落在被测查询的时间窗内（query_ms 很短的样本不能声称"覆盖查询期"）。
	sampleTimes []int64
	maxGap      time.Duration
	last        time.Time
	peak        int64
	note        string
}

// loop 按固定间隔采样直到 done 关闭（子进程退出）。
func (s *procSampler) loop(done <-chan struct{}) {
	fdDir := fmt.Sprintf("/proc/%d/fd", s.pid)
	s.sampleOnce(fdDir) // 立即一次：短查询也至少有一个覆盖点
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	s.last = time.Now()
	for {
		select {
		case <-done:
			return
		case now := <-ticker.C:
			if gap := now.Sub(s.last); gap > s.maxGap {
				s.maxGap = gap
			}
			s.last = now
			s.sampleOnce(fdDir)
		}
	}
}

// sampleOnce 统计一次：/proc/<pid>/fd 下目标在 tmpDir 内的 fd 大小之和。
func (s *procSampler) sampleOnce(fdDir string) {
	ts := time.Now().UnixNano()
	entries, err := os.ReadDir(fdDir)
	if err != nil {
		// 进程退出后 /proc/<pid> 消失：保留此前的有效观测，只记原因。
		s.note = fmt.Sprintf("read %s: %v", fdDir, err)
		return
	}
	var total int64
	for _, e := range entries {
		fdPath := filepath.Join(fdDir, e.Name())
		target, err := os.Readlink(fdPath)
		if err != nil {
			continue
		}
		// 目标可能是 "/tmp/xxx/etilqs_abc (deleted)"——前缀判断仍成立。
		if !strings.HasPrefix(target, s.tmpDir) {
			continue
		}
		fi, err := os.Stat(fdPath)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		total += fi.Size()
	}
	s.samples++
	s.okSamples++
	s.sampleTimes = append(s.sampleTimes, ts)
	if total > s.peak {
		s.peak = total
	}
	s.note = ""
}

// benchTempInterval 临时文件采样间隔（5ms：深递归在 4096 节点上的查询是
// 百毫秒级，5ms 能覆盖整个过程；更小的间隔本身会抬高被测量进程的噪声）。
const benchTempInterval = time.Millisecond // todo23：短查询覆盖（子进程内另有 250µs 采样）

var gnuTimeRSS = regexp.MustCompile(`Maximum resident set size \(kbytes\):\s*(\d+)`)

// parseGNUTimeMaxRSS 解析 `/usr/bin/time -v` 输出里的进程峰值 RSS。
func parseGNUTimeMaxRSS(data []byte) (int64, bool) {
	m := gnuTimeRSS.FindSubmatch(data)
	if m == nil {
		return 0, false
	}
	kb, err := strconv.ParseInt(string(m[1]), 10, 64)
	if err != nil {
		return 0, false
	}
	return kb * 1024, true
}

var benchTimeTool struct {
	once sync.Once
	path string
	ok   bool
}

// newBenchCmd 构造最小 exec.Cmd（基准只关心 argv 与 exit code）。
func newBenchCmd(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}

// gnuTimeTool 探测 `/usr/bin/time -v` 是否可用（macOS 的 time 无 -v）。
// 不可用时基准仍可跑：子进程自报 VmHWM 顶替，并标注来源。
func gnuTimeTool() (string, bool) {
	benchTimeTool.once.Do(func() {
		for _, p := range []string{"/usr/bin/time", "/bin/time"} {
			if _, err := os.Stat(p); err != nil {
				continue
			}
			out, err := os.CreateTemp("", "walkbench-time-")
			if err != nil {
				continue
			}
			name := out.Name()
			out.Close()
			cmd := newBenchCmd(p, "-v", "-o", name, "/bin/true")
			runErr := cmd.Run()
			data, _ := os.ReadFile(name)
			os.Remove(name)
			if runErr == nil {
				if _, ok := parseGNUTimeMaxRSS(data); ok {
					benchTimeTool.path, benchTimeTool.ok = p, true
					return
				}
			}
		}
	})
	return benchTimeTool.path, benchTimeTool.ok
}

// benchEnv 过滤掉继承里的 TMPDIR/TMP/TEMP 后追加 extra（同名环境变量重复
// 时不同 libc 取第一条，不能靠"后写覆盖"）。
func benchEnv(extra map[string]string) []string {
	out := make([]string, 0, len(os.Environ())+len(extra))
	for _, kv := range os.Environ() {
		key := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			key = kv[:i]
		}
		if key == "TMPDIR" || key == "TMP" || key == "TEMP" {
			continue
		}
		out = append(out, kv)
	}
	for k, v := range extra {
		out = append(out, k+"="+v)
	}
	return out
}
