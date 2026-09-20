package sqlite

import (
	"sync"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// Q252d：全边集邻接表进程内缓存（每次 GetPath 都要扫全表建 map——
// go2o 实测 77-110ms/次，成本几乎全在扫表+建 map，BFS 本身很小）。

// seedEdgeGraphRepo 造 a→b→c 链 + build_metadata（build_id 存在才缓存）。
func seedEdgeGraphRepo(t *testing.T) *Repo {
	t.Helper()
	r := newTestRepo(t)
	nodes := []*domain.CodeEntity{
		pathNode("symbol:go:example.com/m:a", "a", "a.go", 1),
		pathNode("symbol:go:example.com/m:b", "b", "b.go", 2),
		pathNode("symbol:go:example.com/m:c", "c", "c.go", 3),
		pathNode("symbol:go:example.com/m:d", "d", "d.go", 4),
	}
	edges := []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:a", TargetID: "symbol:go:example.com/m:b",
			Kind: domain.FactDataFlowsTo, ToolSource: domain.ToolSSA, Confidence: 1},
		{SourceID: "symbol:go:example.com/m:b", TargetID: "symbol:go:example.com/m:c",
			Kind: domain.FactDataFlowsTo, ToolSource: domain.ToolSSA, Confidence: 1},
	}
	save(t, r, nodes, edges)
	// build_metadata：build_id 决定缓存键（构建流程里由 materialize 写入）
	// 注意 error_message 必须非 NULL（GetLatest 直接 Scan，不 COALESCE——
	// 生产写入路径 SaveBuildMetadata 总是写空串）
	if _, err := r.Exec(`INSERT INTO build_metadata (build_id, commit_sha, tool_name, status, timestamp, duration_ms, error_message)
		VALUES ('build-1', 'deadbeef', 'test', 'success', strftime('%s','now'), 0, '')`); err != nil {
		t.Fatal(err)
	}
	return r
}

// TestEdgeGraphCacheHitAndInvalidation：同 build_id 内命中缓存（库外改动不可见），
// build_id 变化即失效（重读）。
func TestEdgeGraphCacheHitAndInvalidation(t *testing.T) {
	r := seedEdgeGraphRepo(t)
	if path, err := r.GetPath("symbol:go:example.com/m:a", "symbol:go:example.com/m:c", 8, false); err != nil || len(path) == 0 {
		t.Fatalf("a→c 应可达，err=%v len=%d", err, len(path))
	}
	// 直接改库（绕过构建）：同 build_id 下缓存命中 → b→d 边不可见
	insertEdgeRawDirect(t, r, "symbol:go:example.com/m:b", "symbol:go:example.com/m:d", "data_flows_to")
	if path, err := r.GetPath("symbol:go:example.com/m:b", "symbol:go:example.com/m:d", 8, false); err != nil || len(path) != 0 {
		t.Errorf("同 build_id 应命中缓存（新边不可见），err=%v len=%d", err, len(path))
	}
	// build_id 变化 → 缓存失效 → 新边可见
	if _, err := r.Exec(`UPDATE build_metadata SET build_id = 'build-2'`); err != nil {
		t.Fatal(err)
	}
	if path, err := r.GetPath("symbol:go:example.com/m:b", "symbol:go:example.com/m:d", 8, false); err != nil || len(path) == 0 {
		t.Errorf("build_id 变化后应重读（b→d 可见），err=%v len=%d", err, len(path))
	}
}

// TestEdgeGraphKindViews：缓存按 kind 集合切视图（数据流集与调用集互不串味）。
func TestEdgeGraphKindViews(t *testing.T) {
	r := seedEdgeGraphRepo(t)
	// 再加一条 calls 边（a→d），它不应出现在数据流路径查询里
	insertEdgeRawDirect(t, r, "symbol:go:example.com/m:a", "symbol:go:example.com/m:d", "calls")
	if path, err := r.GetPath("symbol:go:example.com/m:a", "symbol:go:example.com/m:d", 8, false); err != nil || len(path) != 0 {
		t.Errorf("calls 边不应出现在数据流路径查询，err=%v len=%d", err, len(path))
	}
	if path, err := r.GetPath("symbol:go:example.com/m:a", "symbol:go:example.com/m:d", 8, true); err != nil || len(path) == 0 {
		t.Errorf("viaCalls 应经 calls 边可达，err=%v len=%d", err, len(path))
	}
}

// TestShouldCacheEdgeGraph 阈值判定（大图不缓存，防小内存机器再叠一份图）。
func TestShouldCacheEdgeGraph(t *testing.T) {
	if !shouldCacheEdgeGraph(0) || !shouldCacheEdgeGraph(maxCachedEdgeCount) {
		t.Error("阈值内应缓存")
	}
	if shouldCacheEdgeGraph(maxCachedEdgeCount + 1) {
		t.Error("超阈值不应缓存")
	}
}

// TestEdgeGraphConcurrentPaths 并发查询（-race 下验证只读共享安全）。
func TestEdgeGraphConcurrentPaths(t *testing.T) {
	r := seedEdgeGraphRepo(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				if _, err := r.GetPath("symbol:go:example.com/m:a", "symbol:go:example.com/m:c", 8, false); err != nil {
					t.Errorf("并发 GetPath: %v", err)
					return
				}
				if _, err := r.GetPath("symbol:go:example.com/m:a", "symbol:go:example.com/m:c", 8, true); err != nil {
					t.Errorf("并发 GetPath(viaCalls): %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// insertEdgeRawDirect 绕过写路径直插一条边（Q254c：整数代理键——端点
// canonical ID 先解析成 id_int）。用于验证"库外改动 → 缓存命中不可见"。
func insertEdgeRawDirect(t *testing.T, r *Repo, src, dst domain.CanonicalID, kind string) {
	t.Helper()
	var sRef, dRef int64
	if err := r.QueryRow("SELECT id_int FROM nodes WHERE id = ?", string(src)).Scan(&sRef); err != nil {
		t.Fatalf("resolve %s: %v", src, err)
	}
	if err := r.QueryRow("SELECT id_int FROM nodes WHERE id = ?", string(dst)).Scan(&dRef); err != nil {
		t.Fatalf("resolve %s: %v", dst, err)
	}
	if _, err := r.Exec(`INSERT INTO edges(source_ref, target_ref, kind, tool_source, confidence, count)
		VALUES(?, ?, ?, 'ssa', 1.0, 1)`, sRef, dRef, kind); err != nil {
		t.Fatal(err)
	}
}
