package sqlite

import (
	"strconv"
	"sync"
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

// TestBuildWriteCacheLookupPublish：命中/未命中分离 + 统计。
func TestBuildWriteCacheLookupPublish(t *testing.T) {
	c := newBuildWriteCache(10)
	c.publish(map[string]int64{"a": 1, "b": 2})
	hits, miss := c.lookup([]string{"a", "b", "c"})
	if hits["a"] != 1 || hits["b"] != 2 {
		t.Errorf("hits = %v, want a=1 b=2", hits)
	}
	if len(miss) != 1 || miss[0] != "c" {
		t.Errorf("miss = %v, want [c]", miss)
	}
	st := c.snapshot()
	if st.Inputs != 3 || st.Hits != 2 || st.Writes != 2 || st.Size != 2 {
		t.Errorf("stats = %+v, want inputs=3 hits=2 writes=2 size=2", st)
	}
}

// TestBuildWriteCacheCapacityClear：容量溢出整表清空并计数（P2 再换淘汰）。
func TestBuildWriteCacheCapacityClear(t *testing.T) {
	c := newBuildWriteCache(2)
	c.publish(map[string]int64{"a": 1, "b": 2})
	c.publish(map[string]int64{"c": 3})
	st := c.snapshot()
	if st.Clears != 1 || st.Size != 1 {
		t.Errorf("stats = %+v, want clears=1 size=1", st)
	}
	if _, miss := c.lookup([]string{"a"}); len(miss) != 1 {
		t.Errorf("清表后 a 应未命中")
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
		if _, ok := c.refs["symbol:go:x:missing"]; ok {
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
	if _, _, err := r.resolveNodeRefsCached(tx, []string{"symbol:go:x:a"}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if c := r.currentBuildWriteCache(); len(c.refs) != 0 {
		t.Errorf("回滚后缓存应为空, got %d", len(c.refs))
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

// TestBuildWriteCacheConcurrent：并发 lookup/publish 无数据竞争（-race 验证）。
func TestBuildWriteCacheConcurrent(t *testing.T) {
	c := newBuildWriteCache(64)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				id := string(rune('a' + (seed+i)%8))
				c.lookup([]string{id})
				c.publish(map[string]int64{id: int64(i)})
			}
		}(w)
	}
	wg.Wait()
	if st := c.snapshot(); st.Inputs != 8*500 {
		t.Errorf("inputs = %d, want %d", st.Inputs, 8*500)
	}
}

func benchRepo(b *testing.B, ids []string) *Repo {
	b.Helper()
	db, err := Open(b.TempDir())
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	b.Cleanup(func() { db.Close() })
	r := NewRepo(db)
	nodes := make([]*domain.CodeEntity, 0, len(ids))
	for _, id := range ids {
		nodes = append(nodes, node(id, "function", "n", "a.go"))
	}
	if _, err := r.SaveBatchStats(nodes, nil, nil); err != nil {
		b.Fatalf("save nodes: %v", err)
	}
	return r
}

func benchIDs(n int) []string {
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		ids = append(ids, "symbol:go:x:n"+strconv.Itoa(i))
	}
	return ids
}

// BenchmarkEndpointResolveUncached：每批直接查库解析端点（对照基线）。
func BenchmarkEndpointResolveUncached(b *testing.B) {
	ids := benchIDs(512)
	r := benchRepo(b, ids)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tx, err := r.Begin()
		if err != nil {
			b.Fatal(err)
		}
		if _, err := resolveNodeRefs(tx, ids); err != nil {
			b.Fatal(err)
		}
		_ = tx.Rollback()
	}
}

// BenchmarkEndpointResolveCached：命中构建期正向缓存（不查库）。
func BenchmarkEndpointResolveCached(b *testing.B) {
	ids := benchIDs(512)
	r := benchRepo(b, ids)
	r.BeginBuildWriteCache()
	defer r.EndBuildWriteCache()
	tx, err := r.Begin()
	if err != nil {
		b.Fatal(err)
	}
	got, err := resolveNodeRefs(tx, ids)
	if err != nil {
		b.Fatal(err)
	}
	_ = tx.Rollback()
	r.currentBuildWriteCache().publish(got)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tx, err := r.Begin()
		if err != nil {
			b.Fatal(err)
		}
		if _, _, err := r.resolveNodeRefsCached(tx, ids); err != nil {
			b.Fatal(err)
		}
		_ = tx.Rollback()
	}
}
