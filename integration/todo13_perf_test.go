//go:build integration

package integration

// todo13-17 A1/A2/A3：固定中立大仓（800 个无关类型 + 3 层调用链）——
// sequence/value-trace 的预算、阶段计数、暖缓存与诊断；不编造加速比。
// 记录样本耗时/次数到日志，断言预算内完成与非零取消。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/cli"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

func TestTodo13QueryBudgetLargeRealIndex(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	t.Setenv("CODEINTEL_SEQ_TIMING", "1")
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("package large\n\n")
	b.WriteString("func Leaf(region string) string { return region }\n")
	b.WriteString("func Mid(region string) string { return Leaf(region) }\n")
	b.WriteString("type Handler struct{ State string }\n")
	b.WriteString("func (h *Handler) Entry(region string) string { h.State = Mid(region); return h.State }\n\n")
	for i := 0; i < 800; i++ {
		fmt.Fprintf(&b, "type Unrelated%d struct{ Cache string }\n", i)
		fmt.Fprintf(&b, "func (u *Unrelated%d) Store() { u.Cache = \"x\"; u.Read() }\n", i)
		fmt.Fprintf(&b, "func (u *Unrelated%d) Read() string { return u.Cache }\n\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/largeperf\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "large.go"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	clearIndex(t, dir)
	start := time.Now()
	if code := runCLI(t, "init", "--repo", dir, "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	t.Logf("index=%s（800 无关类型 + 3 层链）", time.Since(start).Round(time.Millisecond))

	sym := "symbol:go:example.com/largeperf:(Handler).Entry"
	var samples []time.Duration
	var lastRoot map[string]any
	for i := 0; i < 3; i++ {
		start = time.Now()
		code, out := runCLIOut(t, "query", "sequence", sym, "--repo", dir,
			"--code", "--depth", "3", "--json", "--timeout", "2s")
		el := time.Since(start)
		samples = append(samples, el)
		if code != 0 {
			t.Fatalf("sequence exit = %d; %s", code, out)
		}
		if err := json.Unmarshal([]byte(out), &lastRoot); err != nil {
			t.Fatalf("sequence JSON: %v", err)
		}
	}
	t.Logf("sequence samples=%v", samples)
	for _, s := range samples {
		if s > 2*time.Second {
			t.Errorf("固定大仓 sequence 超出 2s 预算: %s", s)
		}
	}
	meta, _ := lastRoot["meta"].(map[string]any)
	tm, _ := meta["timing"].(map[string]any)
	if tm == nil {
		t.Fatalf("--json 应带 timing（CODEINTEL_SEQ_TIMING）: %+v", meta)
	}
	for _, k := range []string{"sym_lookups", "sql_queries", "signature_lookups", "cache_hits"} {
		if n, _ := tm[k].(float64); n <= 0 {
			t.Errorf("阶段计数 %s 应 >0: %+v", k, tm)
		}
	}

	// value-trace：取写节点后冷/暖两次（同一 Actions 实例 → 暖查询命中缓存）
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	acts := action.New(sqlite.NewRepo(db))
	code, out := runCLIOut(t, "query", "trace-backward", "example.com/largeperf.Handler.State",
		"--func", sym, "--repo", dir, "--json")
	if code != 0 {
		t.Fatalf("trace-backward exit=%d; %s", code, out)
	}
	var tr struct {
		Rows []struct {
			ID string `json:"id"`
		} `json:"rows"`
	}
	if err := json.Unmarshal([]byte(out), &tr); err != nil {
		t.Fatal(err)
	}
	var writeID string
	for _, r := range tr.Rows {
		if strings.Contains(r.ID, ".write@") {
			writeID = r.ID
		}
	}
	if writeID == "" {
		t.Fatalf("未找到写节点: %+v", tr.Rows)
	}
	rows1, st1, err := acts.ValueTraceWithContext(t.Context(), domain.CanonicalID(writeID), 8, 1.0, false)
	if err != nil {
		t.Fatal(err)
	}
	rows2, st2, err := acts.ValueTraceWithContext(t.Context(), domain.CanonicalID(writeID), 8, 1.0, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("value-trace cold: rows=%d sql=%.3fms sym=%.3fms hit=%d miss=%d; warm: rows=%d hit=%d miss=%d",
		len(rows1), st1.SQLMS, st1.SymbolMS, st1.CacheHits, st1.CacheMisses,
		len(rows2), st2.CacheHits, st2.CacheMisses)
	if st1.CacheMisses != 1 || st2.CacheHits != 1 || len(rows1) != len(rows2) {
		t.Errorf("暖查询应命中且语义一致: cold=%+v warm=%+v", st1, st2)
	}

	// 预算：1µs value-trace 非零退出（不返回完整成功）
	code, _, stderr := runCLIOutErr(t, "query", "value-trace", writeID, "--repo", dir,
		"--json", "--timeout", "1us")
	if code == 0 || (!strings.Contains(stderr, "预算") && !strings.Contains(stderr, "取消")) {
		t.Errorf("value-trace 1us 预算应失败并说明原因: code=%d\n%s", code, stderr)
	}
}

// runCLIOutErr 捕获 stdout + stderr（预算/取消断言用）。
func runCLIOutErr(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	ro, wo, _ := os.Pipe()
	re, we, _ := os.Pipe()
	os.Stdout, os.Stderr = wo, we
	// todo25 §12：运行期间并发排空 stdout/stderr（大输出不会与 pipe 缓冲互等）。
	var outBuf, errBuf strings.Builder
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(&outBuf, ro)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(&errBuf, re)
		done <- struct{}{}
	}()
	code := cli.Main(context.Background(), args)
	wo.Close()
	we.Close()
	os.Stdout, os.Stderr = oldOut, oldErr
	<-done
	<-done
	return code, outBuf.String(), errBuf.String()
}
