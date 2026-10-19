package sqlite

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

func edgesOf(pairs ...string) []*domain.Fact {
	out := make([]*domain.Fact, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, &domain.Fact{
			SourceID:   domain.CanonicalID(pairs[i]),
			TargetID:   domain.CanonicalID(pairs[i+1]),
			Kind:       domain.FactCalls,
			Confidence: 1.0,
		})
	}
	return out
}

// TestBuildWriteCachePartition：命中/未命中分离（命中直接写入 refs）+ 统计。
func TestBuildWriteCachePartition(t *testing.T) {
	c := newBuildWriteCache(10)
	c.publish(map[string]int64{"a": 1, "b": 2})
	refs := map[string]int64{}
	miss := c.partition(refs, []string{"a", "b", "c"})
	if refs["a"] != 1 || refs["b"] != 2 {
		t.Errorf("refs = %v, want a=1 b=2", refs)
	}
	if len(miss) != 1 || miss[0] != "c" {
		t.Errorf("miss = %v, want [c]", miss)
	}
	st := c.snapshot()
	if st.Inputs != 3 || st.Hits != 2 || st.Writes != 2 || st.Size != 2 {
		t.Errorf("stats = %+v, want inputs=3 hits=2 writes=2 size=2", st)
	}
}

// TestBuildWriteCacheSegmentedEviction：cur 达 capacity/2 时分段轮换——
// 上一代命中的键晋升保留，更早的键被淘汰（非整表清空）。
func TestBuildWriteCacheSegmentedEviction(t *testing.T) {
	c := newBuildWriteCache(4)                  // half=2
	c.publish(map[string]int64{"a": 1, "b": 2}) // cur={a,b} 满
	c.publish(map[string]int64{"c": 3})         // 轮换：prev={a,b}, cur={c}
	// a 在 prev 中命中并晋升
	refs := map[string]int64{}
	if miss := c.partition(refs, []string{"a"}); len(miss) != 0 || refs["a"] != 1 {
		t.Errorf("a 应在 prev 命中并返回，miss=%v refs=%v", miss, refs)
	}
	// c 在 cur 中
	if miss := c.partition(map[string]int64{}, []string{"c"}); len(miss) != 0 {
		t.Errorf("c 应在 cur 命中")
	}
	st := c.snapshot()
	if st.Rotations != 1 {
		t.Errorf("rotations = %d, want 1", st.Rotations)
	}
	if st.Size > 4 {
		t.Errorf("size = %d, 不得超过 capacity", st.Size)
	}
}

// TestResolveNodeRefsCachedHit：第二批共享端点应命中缓存（不重复查库）。
func TestResolveNodeRefsCachedHit(t *testing.T) {
	r := newTestRepo(t)
	a, b, c := node("symbol:go:x:a", "function", "a", "a.go"),
		node("symbol:go:x:b", "function", "b", "b.go"),
		node("symbol:go:x:c", "function", "c", "c.go")
	save(t, r, []*domain.CodeEntity{a, b, c}, nil)

	r.BeginBuildWriteCache()
	defer r.EndBuildWriteCache()
	if _, err := r.SaveBatchStats(nil, edgesOf("symbol:go:x:a", "symbol:go:x:b"), nil); err != nil {
		t.Fatalf("batch1: %v", err)
	}
	if _, err := r.SaveBatchStats(nil, edgesOf("symbol:go:x:a", "symbol:go:x:c"), nil); err != nil {
		t.Fatalf("batch2: %v", err)
	}
	st := r.EndBuildWriteCache()
	if st.Hits < 1 {
		t.Errorf("第二批应命中缓存端点 a，stats = %+v", st)
	}
	if st.Writes < 3 {
		t.Errorf("应发布 a/b/c 三个正向键，stats = %+v", st)
	}
	n, cnt, err := r.Counts()
	if err != nil || n != 3 || cnt != 2 {
		t.Errorf("Counts = (%d,%d,%v), want (3,2,nil)", n, cnt, err)
	}
}

// TestResolveNodeRefsCachedMissingNotCached：缺失端点不缓存——节点落库后
// 下一批必须能重新解析到（否则边永久丢失）。
func TestResolveNodeRefsCachedMissingNotCached(t *testing.T) {
	r := newTestRepo(t)
	a := node("symbol:go:x:a", "function", "a", "a.go")
	save(t, r, []*domain.CodeEntity{a}, nil)

	r.BeginBuildWriteCache()
	res, err := r.SaveBatchStats(nil, edgesOf("symbol:go:x:missing", "symbol:go:x:a"), nil)
	if err != nil {
		t.Fatalf("batch1: %v", err)
	}
	if len(res.FailedEdges) != 1 {
		t.Fatalf("缺失端点边应 FailedEdges=1, got %d", len(res.FailedEdges))
	}
	if c := r.currentBuildWriteCache(); c != nil {
		if miss := c.partition(map[string]int64{}, []string{"symbol:go:x:missing"}); len(miss) != 1 {
			t.Fatal("缺失端点不得缓存")
		}
	}
	// missing 节点落库后（缓存启用中），下一批应解析到并成功插入。
	save(t, r, []*domain.CodeEntity{node("symbol:go:x:missing", "function", "missing", "m.go")}, nil)
	if _, err := r.SaveBatchStats(nil, edgesOf("symbol:go:x:missing", "symbol:go:x:a"), nil); err != nil {
		t.Fatalf("batch2: %v", err)
	}
	r.EndBuildWriteCache()
	n, cnt, err := r.Counts()
	if err != nil || n != 2 || cnt != 1 {
		t.Errorf("Counts = (%d,%d,%v), want (2,1,nil)", n, cnt, err)
	}
}

// TestResolveNodeRefsRollbackDoesNotPublish：事务回滚（未提交）不得发布缓存。
func TestResolveNodeRefsRollbackDoesNotPublish(t *testing.T) {
	r := newTestRepo(t)
	a := node("symbol:go:x:a", "function", "a", "a.go")
	save(t, r, []*domain.CodeEntity{a}, nil)

	r.BeginBuildWriteCache()
	defer r.EndBuildWriteCache()
	tx, err := r.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, _, err := r.resolveNodeRefsCached(tx, edgesOf("symbol:go:x:a", "")); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if sz := r.currentBuildWriteCache().snapshot().Size; sz != 0 {
		t.Errorf("回滚后缓存应为空, got %d", sz)
	}
	if st := r.EndBuildWriteCache(); st.Writes != 0 {
		t.Errorf("回滚后 writes 应为 0, got %+v", st)
	}
}

// TestBuildWriteCacheRebuildIsolation：End 后再 Begin 是全新缓存（不跨构建复用）。
func TestBuildWriteCacheRebuildIsolation(t *testing.T) {
	r := newTestRepo(t)
	a, b := node("symbol:go:x:a", "function", "a", "a.go"), node("symbol:go:x:b", "function", "b", "b.go")
	save(t, r, []*domain.CodeEntity{a, b}, nil)

	r.BeginBuildWriteCache()
	if _, err := r.SaveBatchStats(nil, edgesOf("symbol:go:x:a", "symbol:go:x:b"), nil); err != nil {
		t.Fatal(err)
	}
	if st := r.EndBuildWriteCache(); st.Writes == 0 {
		t.Fatal("第一次构建应发布正向键")
	}
	r.BeginBuildWriteCache()
	st := r.EndBuildWriteCache()
	if st.Writes != 0 || st.Hits != 0 || st.Size != 0 {
		t.Errorf("重建应得到全新缓存, got %+v", st)
	}
}

// TestResolveBeyondCapacityStillCorrect：缓存超限（远小于端点规模）后，
// 查询仍必须通过回源解析得到正确 refs——淘汰只影响命中率，不影响语义。
func TestResolveBeyondCapacityStillCorrect(t *testing.T) {
	r := newTestRepo(t)
	ids := benchIDs(12)
	nodes := make([]*domain.CodeEntity, 0, len(ids))
	for _, id := range ids {
		nodes = append(nodes, node(id, "function", "n", "a.go"))
	}
	save(t, r, nodes, nil)

	r.BeginBuildWriteCache()
	r.buildWrite = newBuildWriteCache(4) // 人为缩小容量，制造持续淘汰
	defer r.EndBuildWriteCache()
	edges := make([]*domain.Fact, 0, len(ids)/2)
	for i := 0; i+1 < len(ids); i += 2 {
		edges = append(edges, &domain.Fact{
			SourceID: domain.CanonicalID(ids[i]), TargetID: domain.CanonicalID(ids[i+1]),
			Kind: domain.FactCalls, Confidence: 1.0,
		})
	}
	if _, err := r.SaveBatchStats(nil, edges, nil); err != nil {
		t.Fatalf("SaveBatchStats: %v", err)
	}
	if st := r.currentBuildWriteCache().snapshot(); st.Size > 4 {
		t.Errorf("缓存规模 = %d, 不得超过容量 4", st.Size)
	}
	if _, cnt, err := r.Counts(); err != nil || cnt != len(edges) {
		t.Errorf("超限后边数 = %d (err=%v), want %d（淘汰不得丢边）", cnt, err, len(edges))
	}
}

// TestResolveEndpointCacheInvalidation：缓存只在构建生命周期内有效——
// End 后再 Begin 不复用旧键（重建隔离，超限/失效同口径）。
func TestResolveEndpointCacheInvalidation(t *testing.T) {
	r := newTestRepo(t)
	a := node("symbol:go:x:a", "function", "a", "a.go")
	b := node("symbol:go:x:b", "function", "b", "b.go")
	save(t, r, []*domain.CodeEntity{a, b}, nil)

	r.BeginBuildWriteCache()
	if _, err := r.SaveBatchStats(nil, edgesOf("symbol:go:x:a", "symbol:go:x:b"), nil); err != nil {
		t.Fatal(err)
	}
	if st := r.EndBuildWriteCache(); st.Writes == 0 {
		t.Fatal("第一次构建应写入缓存")
	}
	// 新构建：缓存键不得继承（即便 DB 行仍在，也必须回源）
	r.BeginBuildWriteCache()
	if st := r.EndBuildWriteCache(); st.Hits != 0 || st.Size != 0 {
		t.Errorf("新构建不得复用旧缓存, got %+v", st)
	}
}

// TestResolveNodeRefsCachedOmitsUnresolved（todo9-4）：refs 兼作 seen 后，
// 未解析端点（哨兵 0）必须从结果里删除——否则调用方会把 0 当有效 rowid。
func TestResolveNodeRefsCachedOmitsUnresolved(t *testing.T) {
	r := newTestRepo(t)
	r.BeginBuildWriteCache()
	defer r.EndBuildWriteCache()
	save(t, r, []*domain.CodeEntity{node("a", "function", "A", "a.go")}, nil)

	tx, err := r.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	refs, _, err := r.resolveNodeRefsCached(tx, edgesOf("a", "missing", "a", "a"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := refs["missing"]; ok {
		t.Fatalf("未解析端点不应留在 refs（哨兵 0）: %v", refs)
	}
	if refs["a"] == 0 {
		t.Fatalf("已解析端点 rowid 异常: %v", refs)
	}
}
