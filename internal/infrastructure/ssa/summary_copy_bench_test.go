package ssa

import (
	"sync"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// mergeFuncDataLegacy 旧实现（无预分配）——对照测量中间复制成本。
func mergeFuncDataLegacy(fdMu *sync.Mutex, data map[domain.CanonicalID]*funcData,
	owner domain.CanonicalID, fd *funcData) {
	if owner == "" || fd == nil || len(fd.directReads)+len(fd.directWrites)+len(fd.calls)+len(fd.indirectWrites) == 0 {
		return
	}
	fdMu.Lock()
	defer fdMu.Unlock()
	d := data[owner]
	if d == nil {
		d = &funcData{}
		data[owner] = d
	}
	d.directReads = append(d.directReads, fd.directReads...)
	d.directWrites = append(d.directWrites, fd.directWrites...)
	d.calls = append(d.calls, fd.calls...)
	d.indirectWrites = append(d.indirectWrites, fd.indirectWrites...)
}

func benchFD(n int) *funcData {
	fd := &funcData{}
	for i := 0; i < n; i++ {
		fd.directReads = append(fd.directReads, fieldEntry{fieldPath: "T.X", instancePath: "t.x", line: i})
		fd.calls = append(fd.calls, callInfo{calleeID: "symbol:go:m:g", callLine: i})
	}
	return fd
}

// BenchmarkMergeFuncDataPrealloc：块/全局集合合并（预分配版）。
func BenchmarkMergeFuncDataPrealloc(b *testing.B) {
	const owners, per = 300, 20
	src := benchFD(per)
	var mu sync.Mutex
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		data := make(map[domain.CanonicalID]*funcData, owners)
		for j := 0; j < owners; j++ {
			owner := domain.CanonicalID("symbol:go:m:f" + itoaPad(j))
			mergeFuncData(&mu, data, owner, src)
		}
	}
}

// BenchmarkMergeFuncDataLegacy：同上的旧实现对照。
func BenchmarkMergeFuncDataLegacy(b *testing.B) {
	const owners, per = 300, 20
	src := benchFD(per)
	var mu sync.Mutex
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		data := make(map[domain.CanonicalID]*funcData, owners)
		for j := 0; j < owners; j++ {
			owner := domain.CanonicalID("symbol:go:m:f" + itoaPad(j))
			mergeFuncDataLegacy(&mu, data, owner, src)
		}
	}
}

// BenchmarkToCachedFD：摘要 → 缓存 DTO 转换分配。
func BenchmarkToCachedFD(b *testing.B) {
	fd := benchFD(200)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = toCachedFD(fd)
	}
}
