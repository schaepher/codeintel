package sqlite

// todo22-14.4-B：递归查询**资源基准**（父进程编排 + 夹具 + 样本校验）。
//
// 跑法（一条完整命令，输出目录自选；默认写 t.TempDir()）：
//
//	CODEINTEL_WALK_BENCH=1 CODEINTEL_WALK_BENCH_OUT="$PWD/.tmp/walkbench" \
//	  go test ./internal/infrastructure/sqlite \
//	  -run '^TestWalkEdgesResourceBench$' -count=1 -v -timeout 30m
//
// 可选：CODEINTEL_WALK_BENCH_REPS=N（每组样本数，默认 3）。
//
// 结构：父进程建两档夹具（N=256/4096，经生产 SaveBatchStats 写入，断言
// nodes=N、edges=3N），然后**串行**拉起子进程（每次一个样本，见
// repo_walk_bench_child_test.go）执行查询；峰值由父进程侧测量（见
// repo_walk_bench_proc_test.go）。三条 SQL 变体全部来自生产代码
// （walkDirectQuery / walkRecursiveQuery 的 materialized=false/true），
// 同一数据库、同一参数、同一六列结果，不另复制 SQL 文本。
//
// 期望边数（DISTINCT 后；原递归与物化两档都必须命中）由
// benchExpectedEdges 固定，结果按规范化摘要逐项对照——只比条数不够。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
)

// benchExpectedEdges N → depth → DISTINCT 后的期望边数。
var benchExpectedEdges = map[int]map[int]int{
	256:  {1: 3, 2: 12, 8: 66, 32: 282, 128: 768},
	4096: {1: 3, 2: 12, 8: 66, 32: 282, 128: 1146},
}

var benchDepths = []int{1, 2, 8, 32, 128}

type benchFixture struct {
	N       int
	Dir     string
	Nodes   int
	Edges   int
	BuildMS float64
}

// TestWalkEdgesResourceBench 基准入口（默认 skip，需显式开启）。
func TestWalkEdgesResourceBench(t *testing.T) {
	if os.Getenv(benchEnableEnv) != "1" {
		t.Skip("资源基准默认跳过：CODEINTEL_WALK_BENCH=1 go test ./internal/infrastructure/sqlite -run '^TestWalkEdgesResourceBench$' -count=1 -v")
	}
	reps := 3
	if v := os.Getenv(benchRepsEnv); v != "" {
		got, err := strconv.Atoi(v)
		if err != nil || got < 1 {
			t.Fatalf("%s=%q 非法", benchRepsEnv, v)
		}
		reps = got
	}
	outDir := os.Getenv(benchOutEnv)
	if outDir == "" {
		outDir = t.TempDir()
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("create out dir: %v", err)
	}
	bin, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	t.Logf("基准输出目录: %s", outDir)

	meta := collectBenchMeta(t, reps)
	samplesPath := filepath.Join(outDir, "samples.jsonl")
	f, err := os.Create(samplesPath)
	if err != nil {
		t.Fatalf("create samples.jsonl: %v", err)
	}
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)

	var samples []*benchSample
	order := 0
	var lastFixture *benchFixture
	for _, n := range []int{256, 4096} {
		fx := buildWalkBenchFixture(t, n)
		lastFixture = fx
		meta.Builds = append(meta.Builds, benchBuildMeta{
			N: fx.N, Nodes: fx.Nodes, Edges: fx.Edges, BuildMS: fx.BuildMS})
		t.Logf("夹具 N=%d: nodes=%d edges=%d build=%.1fms", fx.N, fx.Nodes, fx.Edges, fx.BuildMS)
		cold := true
		for _, depth := range benchDepths {
			for _, variant := range benchVariantsFor(depth) {
				for s := 0; s < reps; s++ {
					class := "warm_repeat"
					if cold {
						class, cold = "cold_first_query", false
					}
					smp := runBenchSample(t, bin, outDir, fx, depth, variant, s, class, order)
					order++
					samples = append(samples, smp)
					if err := enc.Encode(smp); err != nil {
						t.Fatalf("write samples.jsonl: %v", err)
					}
					if smp.Error != "" {
						t.Errorf("样本失败 N=%d depth=%d %s sample=%d: %s（现场 %s）",
							smp.N, smp.Depth, smp.Variant, smp.Sample, smp.Error, smp.ErrorDir)
					}
				}
			}
		}
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close samples.jsonl: %v", err)
	}
	verifyWalkBenchSamples(t, samples, reps)
	meta.CancelContract = verifyWalkBenchCancelContract(t, lastFixture.Dir)
	meta.SampleCount = len(samples)
	summary := summarizeWalkBench(samples, meta)
	writeWalkBenchSummary(t, outDir, summary)
	t.Logf("样本: %s\n汇总: %s", samplesPath, filepath.Join(outDir, "summary.md"))
}

// buildWalkBenchFixture 建一档夹具（临时 SQLite 图，生产 SaveBatchStats 写入）。
func buildWalkBenchFixture(t *testing.T, n int) *benchFixture {
	t.Helper()
	dir := t.TempDir()
	start := time.Now()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	r := NewRepo(db)

	payload := strings.Repeat("x", 64)
	nodes := make([]*domain.CodeEntity, 0, n)
	edges := make([]*domain.Fact, 0, 3*n)
	for i := 0; i < n; i++ {
		name := benchNodeName(i)
		nodes = append(nodes, node("symbol:go:example.com/materialized:"+name,
			string(domain.KindFunction), name, "flow.go"))
	}
	for i := 0; i < n; i++ {
		for k := 1; k <= 3; k++ {
			edges = append(edges, &domain.Fact{
				SourceID:   domain.CanonicalID("symbol:go:example.com/materialized:" + benchNodeName(i)),
				TargetID:   domain.CanonicalID("symbol:go:example.com/materialized:" + benchNodeName((i+k)%n)),
				Kind:       domain.FactCalls,
				ToolSource: domain.ToolCodeGraph,
				Confidence: 1,
				Metadata:   map[string]any{"line_num": i + 1, "payload": payload},
			})
		}
	}
	save(t, r, nodes, edges)

	var gotNodes, gotEdges int
	if err := db.QueryRow("SELECT COUNT(*) FROM nodes").Scan(&gotNodes); err != nil {
		t.Fatalf("count nodes: %v", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM edges").Scan(&gotEdges); err != nil {
		t.Fatalf("count edges: %v", err)
	}
	if gotNodes != n || gotEdges != 3*n {
		t.Fatalf("夹具产物计数不符: nodes=%d edges=%d，期望 %d/%d（端点必须存在）",
			gotNodes, gotEdges, n, 3*n)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close fixture db: %v", err)
	}
	return &benchFixture{N: n, Dir: dir, Nodes: gotNodes, Edges: gotEdges, BuildMS: benchMS(start)}
}

func benchNodeName(i int) string { return fmt.Sprintf("N%04d", i) }

// runBenchSample 拉起一次子进程并合并测量结果（每次一个样本、串行）。
