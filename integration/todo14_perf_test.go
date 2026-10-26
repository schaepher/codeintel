//go:build integration

package integration

// todo14-17 A1/A2/A3：固定中立大仓的并发分布、查询矩阵、逐阶段停止延迟与
// 资源峰值。报告实测值，不编造加速比/通用阈值。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

func vmsizeKB(t *testing.T, key string) int64 {
	t.Helper()
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, key+":") {
			f := strings.Fields(line)
			if len(f) >= 2 {
				n, _ := strconv.ParseInt(f[1], 10, 64)
				return n
			}
		}
	}
	return 0
}

func TestTodo14MatrixConcurrencyAndStopDelay(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	t.Setenv("CODEINTEL_SEQ_TIMING", "1")
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("package large\n\n")
	b.WriteString("type Doer interface{ Do() }\n")
	b.WriteString("func Leaf(region string) string { return region }\n")
	b.WriteString("func Mid(region string) string { return Leaf(region) }\n")
	b.WriteString("func Callback(f func() string) string { return f() }\n")
	b.WriteString("func NoImpl(d Doer) { d.Do() }\n")
	b.WriteString("type Handler struct{ State string }\n")
	b.WriteString("func (h *Handler) Entry(region string) string {\n")
	b.WriteString("\th.State = Mid(region)\n")
	b.WriteString("\tCallback(func() string { return h.State })\n")
	b.WriteString("\tif region == \"CN\" { h.State = Leaf(region) } else { h.State = Mid(region) }\n")
	b.WriteString("\treturn h.State\n}\n\n")
	// 少量无关类型（矩阵测试不需要 800 个；固定夹具仍可重复）
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, "type U%d struct{ Cache string }\n", i)
		fmt.Fprintf(&b, "func (u *U%d) Store() { u.Cache = \"x\" }\n\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/matrix\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "large.go"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	hwmBefore := vmsizeKB(t, "VmHWM")
	sym := "symbol:go:example.com/matrix:(Handler).Entry"

	// 矩阵：depth 1/2、焦点、未知回调/接口无实现
	matrix := [][]string{
		{"--depth", "1"},
		{"--depth", "2"},
		{"--depth", "3", "--focus", "return"},
		{"--depth", "3", "--focus", "State"},
	}
	for _, extra := range matrix {
		args := append([]string{"query", "sequence", sym, "--repo", dir, "--code", "--json"}, extra...)
		start := time.Now()
		code, out := runCLIOut(t, args...)
		el := time.Since(start)
		if code != 0 {
			t.Fatalf("matrix %v exit=%d; %s", extra, code, out)
		}
		var root map[string]any
		if err := jsonUnmarshal(out, &root); err != nil {
			t.Fatalf("matrix JSON: %v", err)
		}
		meta, _ := root["meta"].(map[string]any)
		tm, _ := meta["timing"].(map[string]any)
		t.Logf("matrix %v: %s nodes=%v sym=%v sql=%v sig=%v cache=%v/%v",
			extra, el.Round(time.Millisecond), tm["nodes"], tm["sym_lookups"],
			tm["sql_queries"], tm["signature_lookups"], tm["cache_hits"], tm["cache_misses"])
	}

	// 并发分布：4 并发 × 2 轮（独立 Actions/连接），记录 p50/max
	var samples []time.Duration
	var mu sync.Mutex
	var wg sync.WaitGroup
	for round := 0; round < 2; round++ {
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				db, err := sqlite.Open(dir)
				if err != nil {
					t.Errorf("open: %v", err)
					return
				}
				defer db.Close()
				acts := action.New(sqlite.NewRepo(db))
				start := time.Now()
				root, err := acts.CodeSequence(action.CodeSequenceRequest{
					Target: sym, RepoAbs: dir, Depth: 3, Evidence: true})
				el := time.Since(start)
				if err != nil || root == nil {
					t.Errorf("concurrent sequence: %v", err)
					return
				}
				mu.Lock()
				samples = append(samples, el)
				mu.Unlock()
			}()
		}
		wg.Wait()
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	if len(samples) == 0 {
		t.Fatal("并发样本为空")
	}
	t.Logf("concurrent sequence n=%d p50=%s max=%s", len(samples),
		samples[len(samples)/2].Round(time.Millisecond), samples[len(samples)-1].Round(time.Millisecond))

	// 停止延迟：1µs 预算（sequence/value-trace）必须快速失败
	start := time.Now()
	code, _, stderr := runCLIOutErr(t, "query", "sequence", sym, "--repo", dir,
		"--code", "--timeout", "1us")
	delay := time.Since(start)
	if code == 0 || (!strings.Contains(stderr, "预算") && !strings.Contains(stderr, "取消")) {
		t.Errorf("sequence 1us 应失败并说明: code=%d\n%s", code, stderr)
	}
	t.Logf("sequence stop delay=%s", delay.Round(time.Microsecond))
	if delay > 2*time.Second {
		t.Errorf("停止延迟超出声明宽限: %s", delay)
	}

	// value-trace：写节点 → 少结果查询 + 1µs 停止
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	repo := sqlite.NewRepo(db)
	writeID := fieldAccessID(t, repo, sym, "h.State", "write")
	db.Close()
	if writeID == "" {
		t.Fatal("未找到 h.State 写节点")
	}
	start = time.Now()
	if code, _, stderr := runCLIOutErr(t, "query", "value-trace", writeID, "--repo", dir,
		"--json", "--timeout", "2s"); code != 0 {
		t.Fatalf("value-trace 预算内应成功: code=%d\n%s", code, stderr)
	}
	t.Logf("value-trace few-result=%s", time.Since(start).Round(time.Millisecond))
	start = time.Now()
	code, _, stderr = runCLIOutErr(t, "query", "value-trace", writeID, "--repo", dir,
		"--json", "--timeout", "1us")
	delay = time.Since(start)
	if code == 0 || (!strings.Contains(stderr, "预算") && !strings.Contains(stderr, "取消")) {
		t.Errorf("value-trace 1us 应失败: code=%d\n%s", code, stderr)
	}
	t.Logf("value-trace stop delay=%s", delay.Round(time.Microsecond))
	if delay > 2*time.Second {
		t.Errorf("value-trace 停止延迟超限: %s", delay)
	}

	hwmAfter := vmsizeKB(t, "VmHWM")
	t.Logf("peak RSS(HWM) before=%dMB after=%dMB", hwmBefore/1024, hwmAfter/1024)
	if hwmAfter > 4*1024*1024 { // >4GB 视为异常增长（保守阈值）
		t.Errorf("峰值 RSS 异常: %dMB", hwmAfter/1024)
	}
}

// jsonUnmarshal 局部辅助（避免与其它测试文件重名）。
func jsonUnmarshal(s string, v any) error {
	return json.Unmarshal([]byte(s), v)
}
