//go:build integration

package integration

// todo16-17：资源采样器能力（GNU/BSD）+ 独立的“工作开始后取消”验收。

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// timeSampler 受支持的资源采样器（GNU -v / BSD -l）；不支持时 ok=false。
type timeSampler struct {
	bin   string
	flag  string
	gnu   bool
	probe string
}

func detectTimeSampler(t *testing.T) (timeSampler, bool) {
	t.Helper()
	bin, err := exec.LookPath("time")
	if err != nil {
		t.Logf("RSS sampling unavailable: time 不可用（platform=%s）", runtime.GOOS)
		return timeSampler{}, false
	}
	for _, cand := range []timeSampler{{bin: bin, flag: "-v", gnu: true}, {bin: bin, flag: "-l"}} {
		cmd := exec.Command(cand.bin, cand.flag, "true")
		if err := cmd.Run(); err == nil {
			return cand, true
		}
	}
	t.Logf("RSS sampling unavailable: time 不支持 -v/-l（platform=%s，sampler=%s）", runtime.GOOS, bin)
	return timeSampler{}, false
}

func parsePeakRSS(out []byte, gnu bool) (int64, bool) {
	if gnu {
		re := regexp.MustCompile(`Maximum resident set size \(kbytes\): (\d+)`)
		if m := re.FindSubmatch(out); m != nil {
			kb, _ := strconv.ParseInt(string(m[1]), 10, 64)
			return kb, kb > 0
		}
		return 0, false
	}
	re := regexp.MustCompile(`(?m)^\s*(\d+)\s+maximum resident set size\s*$`)
	if m := re.FindSubmatch(out); m != nil {
		b, _ := strconv.ParseInt(string(m[1]), 10, 64)
		return b / 1024, b > 0
	}
	return 0, false
}

// TestTodo15TargetRSS 目标查询峰值：采样器能力探测；不可用明确 unavailable，
// 查询自身失败必须报查询失败（不吞成 unavailable）。
func TestTodo15TargetRSS(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := buildTodo15Fixture(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	writeID := fieldAccessID(t, sqlite.NewRepo(db), "symbol:go:example.com/matrix15:(H).Unknown", "h.S", "write")
	db.Close()
	if writeID == "" {
		t.Fatal("未找到写节点")
	}
	wd, _ := os.Getwd()
	bin := filepath.Join(t.TempDir(), "codeintel")
	build := exec.Command("go", "build", "-o", bin, "./cmd/codeintel")
	build.Dir = filepath.Dir(wd)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	sampler, ok := detectTimeSampler(t)
	if !ok {
		t.Log("RSS sampling unavailable：资源验收未完成（保留待验收，不把 0 当成功）")
		return
	}
	cmd := exec.Command(sampler.bin, sampler.flag, bin, "query", "value-trace", writeID,
		"--repo", dir, "--json")
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("目标查询失败（不是 unavailable）: %v\n%s", err, out)
	}
	kb, ok := parsePeakRSS(out, sampler.gnu)
	if !ok {
		t.Logf("RSS sampling unavailable：sampler=%s %s 未解析到峰值（platform=%s）", sampler.bin, sampler.flag, runtime.GOOS)
		return
	}
	t.Logf("target query peak RSS=%dMB; platform=%s; sampler=%s %s; scope=查询进程（不含测试进程）",
		kb/1024, runtime.GOOS, sampler.bin, sampler.flag)
	if kb <= 0 {
		t.Errorf("峰值 RSS 应 >0: %d", kb)
	}
}

// TestTodo15CancelAfterWork 独立于 RSS 采样：预算触发时已有实际工作，记录
// 停止延迟；不依赖资源采样器是否可用。
func TestTodo15CancelAfterWork(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := buildTodo15Fixture(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	writeID := fieldAccessID(t, sqlite.NewRepo(db), "symbol:go:example.com/matrix15:(H).Unknown", "h.S", "write")
	db.Close()
	if writeID == "" {
		t.Fatal("未找到写节点")
	}
	deadline := false
	for _, budget := range []string{"5ms", "2ms", "1ms"} {
		start := time.Now()
		code, _, stderr := runCLIOutErr(t, "query", "value-trace", writeID, "--repo", dir,
			"--json", "--timeout", budget)
		delay := time.Since(start)
		if code != 0 && strings.Contains(stderr, "sql_queries=") {
			re := regexp.MustCompile(`sql_queries=(\d+)`)
			work := re.FindStringSubmatch(stderr)
			n := 0
			if len(work) == 2 {
				n, _ = strconv.Atoi(work[1])
			}
			if n > 0 {
				t.Logf("cancel after work: budget=%s delay=%s %s", budget,
					delay.Round(time.Microsecond), strings.TrimSpace(stderr))
				deadline = true
			}
			if delay > 2*time.Second {
				t.Errorf("取消停止延迟超限: %s", delay)
			}
			break
		}
	}
	if !deadline {
		t.Errorf("未取得“工作已开始后取消”的证据（入口拒绝不算）")
	}
}
