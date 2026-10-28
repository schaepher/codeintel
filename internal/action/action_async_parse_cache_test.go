package action

// T15：边界证明解析缓存（请求内成功/失败都缓存；下一请求重新读取）。

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

func TestAsyncParseCachePerRequest(t *testing.T) {
	dir := t.TempDir()
	src := `package b

func Produce() { }

func Consume() { }
`
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := sqlite.NewRepo(db)
	produceID := domain.CanonicalID("symbol:go:example.com/b:Produce")
	if _, err := repo.SaveBatchStats([]*domain.CodeEntity{
		{ID: produceID, Kind: domain.KindFunction, Name: "Produce", FilePath: "b.go", LineStart: 3},
	}, nil, nil); err != nil {
		t.Fatalf("save: %v", err)
	}
	cr := &countingAsyncReader{Reader: repo}
	acts := New(cr)

	pc := newAsyncParseCache()
	first := acts.parseFuncNodeCached(pc, string(produceID))
	if first == nil || first.decl == nil {
		t.Fatalf("应解析出函数体: %+v", first)
	}
	// 同一请求：同函数只查一次符号（重复解析命中缓存）
	second := acts.parseFuncNodeCached(pc, string(produceID))
	if second != first {
		t.Fatalf("同一请求应复用同一解析结果")
	}
	if cr.symCalls != 1 {
		t.Errorf("同一函数应只解析一次，GetSymbol=%d", cr.symCalls)
	}

	// 失败也缓存：不存在的函数在本次请求只查一次
	missing := "symbol:go:example.com/b:NoSuchFunc"
	if fn := acts.parseFuncNodeCached(pc, missing); fn != nil {
		t.Fatalf("未知函数应返回 nil")
	}
	before := cr.symCalls
	if fn := acts.parseFuncNodeCached(pc, missing); fn != nil {
		t.Fatalf("未知函数应返回 nil")
	}
	if cr.symCalls != before {
		t.Errorf("失败结果也应缓存：GetSymbol 从 %d 增到 %d", before, cr.symCalls)
	}

	// 下一请求：新缓存必须重新读取（索引/源码可能变化）
	pc2 := newAsyncParseCache()
	if fn := acts.parseFuncNodeCached(pc2, string(produceID)); fn == nil {
		t.Fatalf("下一请求应重新解析")
	}
	if cr.symCalls != before+1 {
		t.Errorf("下一请求应重新读符号：GetSymbol=%d，want %d", cr.symCalls, before+1)
	}
}
