package sqlite

// P1-02：全扫类查询的 build_id 投影缓存（repo_chain / repo_flows /
// repo_unused / rg_bfs）——缓存命中/失效/超限/并发/语义等价回归。

import (
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// seedProjectionRepo 造 a→b calls 边 + build_metadata（有 build_id 才缓存）。
func seedProjectionRepo(t *testing.T) *Repo {
	t.Helper()
	r := newTestRepo(t)
	nodes := []*domain.CodeEntity{
		pathNode("symbol:go:example.com/m:a", "a", "a.go", 1),
		pathNode("symbol:go:example.com/m:b", "b", "b.go", 2),
		pathNode("symbol:go:example.com/m:c", "c", "c.go", 3),
	}
	edges := []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:a", TargetID: "symbol:go:example.com/m:b",
			Kind: domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.8},
	}
	save(t, r, nodes, edges)
	if err := r.Save(&domain.BuildMeta{BuildID: "build-1", ToolName: "all", Status: "success"}); err != nil {
		t.Fatalf("Save build: %v", err)
	}
	return r
}

// TestCallFactsProjectionCacheHitAndInvalidation：同一 build_id 内命中缓存
// （库外改动不可见），build_id 变化即失效（重读）。
func TestCallFactsProjectionCacheHitAndInvalidation(t *testing.T) {
	r := seedProjectionRepo(t)
	calls, err := r.GetAllCalls()
	if err != nil || len(calls) != 1 {
		t.Fatalf("首次 GetAllCalls = %d 条, err=%v（want 1）", len(calls), err)
	}
	// 绕过写路径直插新边：同 build_id 下缓存命中 → 不可见
	insertEdgeRawDirect(t, r, "symbol:go:example.com/m:b", "symbol:go:example.com/m:c", "calls")
	if calls, err = r.GetAllCalls(); err != nil || len(calls) != 1 {
		t.Errorf("同 build_id 应命中缓存（新边不可见），got %d 条 err=%v", len(calls), err)
	}
	if n := r.projectionLoadCount(projCallFacts); n != 1 {
		t.Errorf("calls 视图应只扫表一次，实际 %d 次", n)
	}
	// build_id 变化 → 失效重载
	if err := r.Save(&domain.BuildMeta{BuildID: "build-2", ToolName: "incremental", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	if calls, err = r.GetAllCalls(); err != nil || len(calls) != 2 {
		t.Errorf("build_id 变化后应重读（b→c 可见），got %d 条 err=%v", len(calls), err)
	}
	if n := r.projectionLoadCount(projCallFacts); n != 1 {
		// 新 build_id 是新的缓存槽：计数从 0 开始 → 1
		t.Errorf("新 build_id 首次加载计数 = %d，want 1", n)
	}
}

// TestProjectionNoBuildMetaNotCached：无 build_metadata 时不缓存（每次现查，
// 裸库/测试语义与改造前一致）。
func TestProjectionNoBuildMetaNotCached(t *testing.T) {
	r := newTestRepo(t)
	save(t, r, []*domain.CodeEntity{
		pathNode("symbol:go:example.com/m:a", "a", "a.go", 1),
		pathNode("symbol:go:example.com/m:b", "b", "b.go", 2),
		pathNode("symbol:go:example.com/m:c", "c", "c.go", 3),
	}, []*domain.Fact{{
		SourceID: "symbol:go:example.com/m:a", TargetID: "symbol:go:example.com/m:b",
		Kind: domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.8,
	}})
	if calls, err := r.GetAllCalls(); err != nil || len(calls) != 1 {
		t.Fatalf("首次 = %d, err=%v", len(calls), err)
	}
	insertEdgeRawDirect(t, r, "symbol:go:example.com/m:b", "symbol:go:example.com/m:c", "calls")
	if calls, err := r.GetAllCalls(); err != nil || len(calls) != 2 {
		t.Errorf("无 build_metadata 应不缓存（新边可见），got %d err=%v", len(calls), err)
	}
	if n := r.projectionLoadCount(projCallFacts); n != 0 {
		t.Errorf("无 build_id 不应记录缓存加载，实际 %d", n)
	}
}

// TestProjectionSizeCap：行数超上限不缓存（本次仍返回全部行，下次重新加载）。
func TestProjectionSizeCap(t *testing.T) {
	old := maxCachedProjectionRows
	maxCachedProjectionRows = 1
	t.Cleanup(func() { maxCachedProjectionRows = old })

	r := seedProjectionRepo(t)
	if calls, err := r.GetAllCalls(); err != nil || len(calls) != 1 {
		t.Fatalf("首次 = %d, err=%v", len(calls), err)
	}
	// 1 行 ≤ 1 → 缓存；再插一条 → 仍不可见
	insertEdgeRawDirect(t, r, "symbol:go:example.com/m:b", "symbol:go:example.com/m:c", "calls")
	if calls, err := r.GetAllCalls(); err != nil || len(calls) != 1 {
		t.Errorf("阈值内应命中缓存，got %d err=%v", len(calls), err)
	}
	// build_id 换代后 2 行 > 1 → 不缓存：连续两次都重新加载（且都能看到 2 行）
	if err := r.Save(&domain.BuildMeta{BuildID: "build-2", ToolName: "incremental", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if calls, err := r.GetAllCalls(); err != nil || len(calls) != 2 {
			t.Fatalf("第 %d 次（超限不缓存）= %d 行, err=%v（want 2）", i, len(calls), err)
		}
	}
	if n := r.projectionLoadCount(projCallFacts); n != 2 {
		t.Errorf("超限视图应每次重载，loads=%d want 2", n)
	}
}

// TestProjectionConcurrentSingleLoad：并发首次访问只扫表一次，结果稳定。
func TestProjectionConcurrentSingleLoad(t *testing.T) {
	r := seedProjectionRepo(t)
	const n = 16
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			calls, err := r.GetAllCalls()
			if err != nil {
				errs <- err
				return
			}
			if len(calls) != 1 {
				errs <- fmt.Errorf("calls 行数 %d，want 1", len(calls))
			}
			// unused 复用同一 calls 视图
			if _, err := r.GetIsolatedChains(); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("并发查询失败: %v", err)
	}
	if c := r.projectionLoadCount(projCallFacts); c != 1 {
		t.Errorf("并发首次访问应只扫表一次，实际 %d 次", c)
	}
	if c := r.projectionLoadCount(projIsolatedFuncs); c != 1 {
		t.Errorf("函数清单视图应只扫表一次，实际 %d 次", c)
	}
}

// TestImplementsFactsProjection：implements 视图保留 Unimplemented 桩过滤，
// 缓存命中语义不变。
func TestImplementsFactsProjection(t *testing.T) {
	r := newTestRepo(t)
	nodes := []*domain.CodeEntity{
		pathNode("symbol:go:example.com/m:I", "I", "i.go", 1),
		pathNode("symbol:go:example.com/m:impl", "impl", "i.go", 2),
		pathNode("symbol:go:example.com/m:UnimplementedI", "UnimplementedI", "i.go", 3),
		pathNode("symbol:go:example.com/m:real", "real", "i.go", 4),
	}
	edges := []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:I", TargetID: "symbol:go:example.com/m:impl",
			Kind: domain.FactImplements, ToolSource: domain.ToolSCIP, Confidence: 1},
		{SourceID: "symbol:go:example.com/m:I", TargetID: "symbol:go:example.com/m:UnimplementedI",
			Kind: domain.FactImplements, ToolSource: domain.ToolSCIP, Confidence: 1},
	}
	save(t, r, nodes, edges)
	if err := r.Save(&domain.BuildMeta{BuildID: "b1", ToolName: "all", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	got, err := r.GetImplementsEdges()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].TargetID != "symbol:go:example.com/m:impl" {
		t.Fatalf("应只保留真实实现（过滤 Unimplemented 桩），得到 %+v", got)
	}
	// 库外新增 implements 边：缓存命中不可见
	insertEdgeRawDirect(t, r, "symbol:go:example.com/m:I", "symbol:go:example.com/m:real", "implements")
	if got, err = r.GetImplementsEdges(); err != nil || len(got) != 1 {
		t.Errorf("同 build_id 应命中缓存，got %d err=%v", len(got), err)
	}
	if c := r.projectionLoadCount(projImplementsFacts); c != 1 {
		t.Errorf("implements 视图应只扫表一次，实际 %d", c)
	}
}

// TestEdgeTargetKindsProjectionKindOrder：kind 集顺序不影响视图标签（复用），
// 结果只读共享。
func TestEdgeTargetKindsProjectionKindOrder(t *testing.T) {
	r := newTestRepo(t)
	nodes := []*domain.CodeEntity{
		pathNode("symbol:go:example.com/m:a", "a", "a.go", 1),
		pathNode("symbol:go:example.com/m:b", "b", "b.go", 2),
	}
	edges := []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:a", TargetID: "symbol:go:example.com/m:b",
			Kind: domain.FactPassesTo, ToolSource: domain.ToolCodeGraph, Confidence: 0.8},
	}
	save(t, r, nodes, edges)
	if err := r.Save(&domain.BuildMeta{BuildID: "b1", ToolName: "all", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	m1, err := r.edgeTargetKinds("passes_to", "dispatch_to", "initializes")
	if err != nil || !m1["symbol:go:example.com/m:b"] {
		t.Fatalf("首次 = %v, err=%v", m1, err)
	}
	label := projTargetRefs + "dispatch_to,initializes,passes_to"
	if c := r.projectionLoadCount(label); c != 1 {
		t.Fatalf("kind 集视图应只扫表一次，实际 %d", c)
	}
	m2, err := r.edgeTargetKinds("initializes", "passes_to", "dispatch_to")
	if err != nil || !reflect.DeepEqual(m1, m2) {
		t.Errorf("kind 顺序不同应复用同一投影: %v vs %v (err=%v)", m1, m2, err)
	}
	if c := r.projectionLoadCount(label); c != 1 {
		t.Errorf("复用视图不应重复扫表，实际 %d", c)
	}
}

// TestUnusedProjectionReuse：unused 的两个入口（GetUncalledFunctions /
// GetIsolatedChains）共用节点与边投影；缓存命中结果与首次逐字段一致。
func TestUnusedProjectionReuse(t *testing.T) {
	r := seedProjectionRepo(t)
	uncalled1, err := r.GetUncalledFunctions()
	if err != nil {
		t.Fatal(err)
	}
	chains1, err := r.GetIsolatedChains()
	if err != nil {
		t.Fatal(err)
	}
	uncalled2, err := r.GetUncalledFunctions()
	if err != nil {
		t.Fatal(err)
	}
	chains2, err := r.GetIsolatedChains()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(uncalled1, uncalled2) {
		t.Error("GetUncalledFunctions 缓存命中结果应与首次一致")
	}
	if !reflect.DeepEqual(chains1, chains2) {
		t.Error("GetIsolatedChains 缓存命中结果应与首次一致")
	}
	if c := r.projectionLoadCount(projCallFacts); c != 1 {
		t.Errorf("calls 视图应只扫表一次，实际 %d", c)
	}
	// 两个 unused 入口的节点口径不同 → 两个独立视图
	if c := r.projectionLoadCount(projUnusedFuncs); c != 1 {
		t.Errorf("unused 节点视图应只扫表一次，实际 %d", c)
	}
	if c := r.projectionLoadCount(projIsolatedFuncs); c != 1 {
		t.Errorf("孤立链节点视图应只扫表一次，实际 %d", c)
	}
}
