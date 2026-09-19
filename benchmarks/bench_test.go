//go:build benchmark

// Package benchmarks 性能基准（field_trace.md §20.2）：对指定仓库跑
// 进程内 FullBuild，记录各适配器耗时 / 峰值内存 / DB 大小。
// 运行：go test ./benchmarks/ -bench-repo <仓库> [-bench-json]
package benchmarks

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
	"github.com/schaepher/codeintel/internal/orchestrator"
)

var (
	benchRepo = flag.String("bench-repo", ".", "基准构建目标仓库（须含 go.mod）")
	benchJSON = flag.Bool("bench-json", false, "输出 JSON 结构化结果")
)

// result 一次基准构建的结果。
// 注意：peak_rss_bytes 是**进程峰值 RSS**（/proc/self/status VmHWM，含
// test 框架自身）——OOM 判定的直接依据；peak_alloc_bytes 是采样间隙的
// HeapAlloc 峰值（Q246：原来只在构建前后 GC 后各采一次，测不到峰值）。
type result struct {
	Repo       string           `json:"repo"`
	TotalMs    int64            `json:"total_ms"`
	Adapters   map[string]int64 `json:"adapters_ms"`
	PeakAlloc  uint64           `json:"peak_alloc_bytes"`
	PeakRSS    uint64           `json:"peak_rss_bytes"`
	DBBytes    int64            `json:"db_bytes"`
	Nodes      int              `json:"nodes"`
	Edges      int              `json:"edges"`
	Status     string           `json:"status"`
	FinishedAt string           `json:"finished_at"`
}

// peakRSS 读 /proc/self/status 的 VmHWM（进程峰值常驻内存，KB→B）。
// 非 Linux 或无 /proc 时返回 0。
func peakRSS() uint64 {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "VmHWM:") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			return 0
		}
		kb, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil {
			return 0
		}
		return kb * 1024
	}
	return 0
}

// startMemSampler 后台采样 HeapAlloc 峰值（20ms 间隔），返回停止函数
// 与当前峰值读函数。构建是一个秒级过程，采样间隔足够捕到
// emitFunction 阶段的堆峰值。BENCH_MEM_TRACE=1 时每 500ms 往 stderr
// 打一行时间线（配合 [index] 阶段行定位峰值发生在哪一阶段）。
func startMemSampler() (func(), func() uint64) {
	var mu sync.Mutex
	var peak uint64
	trace := os.Getenv("BENCH_MEM_TRACE") != ""
	start := time.Now()
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		lastTrace := time.Now()
		for {
			select {
			case <-done:
				return
			case now := <-ticker.C:
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				mu.Lock()
				if m.HeapAlloc > peak {
					peak = m.HeapAlloc
				}
				mu.Unlock()
				if trace && now.Sub(lastTrace) >= 500*time.Millisecond {
					lastTrace = now
					fmt.Fprintf(os.Stderr, "[bench-mem] t=%.1fs heap=%dMB rss=%dMB\n",
						time.Since(start).Seconds(), m.HeapAlloc>>20, peakRSS()>>20)
				}
			}
		}
	}()
	stopOnce := sync.Once{}
	stop := func() { stopOnce.Do(func() { close(done) }); wg.Wait() }
	read := func() uint64 { mu.Lock(); defer mu.Unlock(); return peak }
	return stop, read
}

// TestBenchmarkFullBuild 对 -bench-repo 执行一次全量构建并输出指标。
func TestBenchmarkFullBuild(t *testing.T) {
	abs, err := filepath.Abs(*benchRepo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(abs, "go.mod")); err != nil {
		t.Fatalf("-bench-repo %s 无 go.mod: %v", abs, err)
	}
	// 记录峰值内存：20ms 采样 HeapAlloc（原实现只在前后 GC 后各采一次，
	// 测不到 emitFunction/SSA 阶段峰值）+ 进程 VmHWM
	stopSampler, peakAlloc := startMemSampler()
	defer stopSampler()

	db, err := sqlite.Open(abs)
	if err != nil {
		t.Fatal(err)
	}
	// Q246：基准必须与 CLI 同构——Repository 需带 ModuleDirs（loadPackages
	// 按 ModuleDirs 逐 module Load；旧代码只设 Modules，ModuleDirs 为空
	// → 一个包都没 Load，AST/SSA 适配器空跑 0ms（"基准数字不可信"的真
	// 凶之一）。与 cli.buildRepo 一致：DiscoverModules 扫描全部 go.mod。
	modules, dirs, err := orchestrator.DiscoverModules(abs)
	if err != nil {
		t.Fatal(err)
	}
	repo := &domain.Repository{Path: abs, Module: modules[0], Modules: modules, ModuleDirs: dirs}
	orch := orchestrator.New(repo, db)
	res, err := orch.FullBuild(context.Background())
	if err != nil {
		t.Fatalf("FullBuild: %v", err)
	}
	stopSampler()
	peak := peakAlloc()
	rss := peakRSS()
	db.Close()

	adapters := map[string]int64{}
	for _, a := range res.Adapter {
		adapters[a.Name] = a.Duration.Milliseconds()
	}
	var dbBytes int64
	if fi, err := os.Stat(filepath.Join(abs, ".codeintel", "codeintel.db")); err == nil {
		dbBytes = fi.Size()
	}
	r := result{
		Repo:       abs,
		TotalMs:    res.Duration.Milliseconds(),
		Adapters:   adapters,
		PeakAlloc:  peak,
		PeakRSS:    rss,
		DBBytes:    dbBytes,
		Nodes:      res.Nodes,
		Edges:      res.Edges,
		Status:     string(res.Status),
		FinishedAt: time.Now().Format("2006-01-02 15:04:05"),
	}
	if *benchJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(r)
		return
	}
	fmt.Printf("构建基准: %s（%s）\n", r.Repo, r.FinishedAt)
	fmt.Printf("  总耗时:   %d ms\n", r.TotalMs)
	fmt.Printf("  适配器:   ")
	for _, a := range res.Adapter {
		fmt.Printf("%s=%dms ", a.Name, a.Duration.Milliseconds())
	}
	fmt.Println()
	fmt.Printf("  峰值内存: %.1f MB（HeapAlloc 采样）  %.1f MB（进程 RSS 峰值）\n",
		float64(r.PeakAlloc)/1024/1024, float64(r.PeakRSS)/1024/1024)
	fmt.Printf("  DB 大小:  %.1f MB（%d 节点 / %d 边）\n", float64(r.DBBytes)/1024/1024, r.Nodes, r.Edges)
	fmt.Printf("  状态:     %s\n", r.Status)
}
