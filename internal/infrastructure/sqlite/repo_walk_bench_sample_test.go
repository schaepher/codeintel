package sqlite

// todo22-14.4-B：基准的**单样本执行**——拉起子进程、采样子进程 TMPDIR 下的
// 临时文件、合并子进程结果与父进程侧峰值测量。编排见
// repo_walk_bench_test.go，测量口径见 repo_walk_bench_proc_test.go。

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func runBenchSample(t *testing.T, bin, outDir string, fx *benchFixture, depth int,
	variant string, sample int, class string, order int) *benchSample {
	t.Helper()
	smp := &benchSample{
		Order: order, N: fx.N, Depth: depth, Variant: variant, Sample: sample,
		SampleClass: class, Nodes: fx.Nodes, Edges: fx.Edges, BuildMS: fx.BuildMS,
		TempSampleIntervalMS: float64(benchTempInterval.Microseconds()) / 1000,
	}
	work, err := os.MkdirTemp(outDir, "run-")
	if err != nil {
		smp.Error = "mkdtemp: " + err.Error()
		return smp
	}
	tmpDir := filepath.Join(work, "tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		smp.Error = "mkdir tmp: " + err.Error()
		smp.ErrorDir = work
		return smp
	}
	resPath := filepath.Join(work, "result.json")
	pidPath := filepath.Join(work, "child.pid")
	timePath := filepath.Join(work, "time.txt")
	logPath := filepath.Join(work, "child.log")

	extraEnv := map[string]string{
		benchChildEnv:    "1",
		benchChildDBEnv:  fx.Dir,
		benchChildNEnv:   strconv.Itoa(fx.N),
		benchChildEnvDS:  strconv.Itoa(depth),
		benchChildVarEnv: variant,
		benchChildSmpEnv: strconv.Itoa(sample),
		benchChildResEnv: resPath,
		benchChildPIDEnv: pidPath,
		"TMPDIR":         tmpDir,
	}
	if v := os.Getenv(benchTempFixtureEnv); v != "" {
		extraEnv[benchTempFixtureEnv] = v
	}
	childEnv := benchEnv(extraEnv)
	childArgs := []string{"-test.run=^TestWalkBenchChild$", "-test.timeout=10m"}

	tool, hasTime := gnuTimeTool()
	var cmd *exec.Cmd
	if hasTime {
		smp.MaxRSSSource = "gnu_time_v"
		cmd = exec.Command(tool, append([]string{"-v", "-o", timePath, bin}, childArgs...)...)
	} else {
		smp.MaxRSSSource = "child_vmhwm"
		cmd = exec.Command(bin, childArgs...)
	}
	cmd.Dir = work
	cmd.Env = childEnv
	logFile, err := os.Create(logPath)
	if err != nil {
		smp.Error = "create log: " + err.Error()
		smp.ErrorDir = work
		return smp
	}
	cmd.Stdout, cmd.Stderr = logFile, logFile
	start := time.Now()
	if err := cmd.Start(); err != nil {
		logFile.Close()
		smp.Error = "start child: " + err.Error()
		smp.ErrorDir = work
		return smp
	}
	pid := cmd.Process.Pid
	if hasTime { // time 包装下 argv 进程不是被测进程，按 pid 文件定位
		if childPid, ok := waitBenchPid(pidPath, 5*time.Second); ok {
			pid = childPid
		}
	}
	sampler := &procSampler{pid: pid, tmpDir: tmpDir, interval: benchTempInterval}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); sampler.loop(stop) }()

	waitErr := cmd.Wait()
	close(stop)
	wg.Wait()
	logFile.Close()

	smp.ProcessWall = benchMS(start)
	smp.ExitCode = cmd.ProcessState.ExitCode()
	smp.TempSamples = sampler.samples
	smp.TempMaxGapMS = float64(sampler.maxGap.Microseconds()) / 1000
	if hasTime {
		data, _ := os.ReadFile(timePath)
		if v, ok := parseGNUTimeMaxRSS(data); ok {
			smp.MaxRSSBytes = benchMeasure{Value: v, OK: true}
		} else {
			smp.MaxRSSNote = "解析 /usr/bin/time -v 的 Maximum resident set size 失败"
			smp.MaxRSSSource = "unavailable"
		}
	}
	if !smp.MaxRSSBytes.OK && smp.MaxRSSSource != "unavailable" {
		smp.MaxRSSSource = "child_vmhwm"
	}

	res := &benchChildResult{}
	resData, resErr := os.ReadFile(resPath)
	if resErr != nil {
		smp.Error = "结果文件缺失: " + resErr.Error()
	} else if err := json.Unmarshal(resData, res); err != nil {
		smp.Error = "解析结果失败: " + err.Error()
	} else {
		smp.OpenMS, smp.QueryMS, smp.CloseMS = res.OpenMS, res.QueryMS, res.CloseMS
		smp.ResultEdges, smp.ResultDigest = res.ResultEdges, res.ResultDigest
		smp.RSSBeforeBytes = res.RSSBefore
		smp.MaxRSSVMHWMBytes = res.VMHWMBytes
		smp.SQLiteVersion, smp.TempStore, smp.CacheSize = res.SQLiteVer, res.TempStore, res.CacheSize
		if res.Error != "" {
			smp.Error = res.Error
		}
	}
	if !smp.MaxRSSBytes.OK && res.VMHWMBytes > 0 {
		smp.MaxRSSBytes = benchMeasure{Value: res.VMHWMBytes, OK: true}
		smp.MaxRSSSource = "child_vmhwm"
	}
	if smp.Error == "" && waitErr != nil {
		smp.Error = "child: " + waitErr.Error()
	}
	if smp.Error == "" && smp.ExitCode != 0 {
		smp.Error = fmt.Sprintf("exit_code=%d", smp.ExitCode)
	}
	// todo23 14.4-B：优先采用子进程内查询生命周期采样（短查询也能可靠覆盖；
	// 含 unlink 仍打开的文件）。父进程轮询只作为回退。
	if res.TempSamples > 0 {
		smp.TempPeakBytes = benchMeasure{Value: res.TempPeak, OK: true}
		smp.TempSamples = res.TempSamples
		smp.TempMaxGapMS = res.TempMaxGapMS
		smp.TempPeakSource = res.TempPeakSource
		smp.TempEvents = res.TempEvents
		if res.TempPeak == 0 {
			smp.TempPeakNote = fmt.Sprintf(
				"查询生命周期内子进程采样 %d 次（最大间隔 %.2fms）未出现 TMPDIR 临时文件：SQLite 留在页缓存/未落盘（有效观测）",
				res.TempSamples, res.TempMaxGapMS)
		}
		if smp.Error == "" {
			os.RemoveAll(work)
		} else {
			smp.ErrorDir = work
		}
		return smp
	}
	// 回退：父进程轮询（采样点必须真的落在查询时间窗内——query_ms 小于
	// 采样间隔的样本不能凭 0 宣称"查询期无临时文件"（记 unavailable）。
	smp.TempPeakSource = "parent_poll"
	inQuery := 0
	if res.QueryEndNS > res.QueryStartNS {
		for _, ts := range sampler.sampleTimes {
			if ts >= res.QueryStartNS && ts <= res.QueryEndNS {
				inQuery++
			}
		}
	}
	switch {
	case sampler.okSamples > 0 && inQuery > 0:
		smp.TempPeakBytes = benchMeasure{Value: sampler.peak, OK: true}
		if sampler.peak == 0 {
			smp.TempPeakNote = fmt.Sprintf(
				"查询期内采样 %d 次（间隔 %.0fms）未出现 TMPDIR 下的临时文件：SQLite 把物化 CTE/临时索引留在页缓存，未落盘（有效观测，非采集失败）",
				inQuery, smp.TempSampleIntervalMS)
		}
	default:
		note := sampler.note
		if sampler.okSamples == 0 {
			note = "采样未取得任何有效快照（/proc 不可用或子进程过早退出）"
			if sampler.note != "" {
				note += "：" + sampler.note
			}
		} else {
			note = fmt.Sprintf("查询时间窗内无采样点（query_ms=%.2f < 采样间隔 %.0fms），无法确认覆盖查询生命周期",
				smp.QueryMS, smp.TempSampleIntervalMS)
		}
		smp.TempPeakBytes = benchMeasure{Note: note}
		smp.TempPeakNote = note
	}
	if smp.Error == "" {
		os.RemoveAll(work) // 成功样本不留现场
	} else {
		smp.ErrorDir = work
	}
	return smp
}

// waitBenchPid 等子进程写出 pid 文件（时间上限内每 1ms 轮询）。
func waitBenchPid(path string, limit time.Duration) (int, bool) {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
				return pid, true
			}
		}
		time.Sleep(time.Millisecond)
	}
	return 0, false
}

// verifyWalkBenchSamples 逐组校验：样本齐全、期望边数、摘要一致（原递归
// 与物化逐项相同；depth=1 的直接一跳同样一致）。
