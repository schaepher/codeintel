package sqlite

// 端点缓存基准与并发回归（从 build_write_cache_test.go 拆出——行数治理）。

import (
	"strconv"
	"sync"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

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
				refs := map[string]int64{}
				c.partition(refs, []string{id})
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
	benchEdges := edgesOf(ids...)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tx, err := r.Begin()
		if err != nil {
			b.Fatal(err)
		}
		if _, _, err := r.resolveNodeRefsCached(tx, benchEdges); err != nil {
			b.Fatal(err)
		}
		_ = tx.Rollback()
	}
}
