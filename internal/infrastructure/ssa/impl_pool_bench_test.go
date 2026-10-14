package ssa

import (
	"go/types"
	"os"
	"path/filepath"
	"testing"
)

// benchImplPool 构建接口池与 Store 接口（基准用）。
func benchImplPool(b *testing.B) (*implTypePool, *types.Named, []*types.Package) {
	b.Helper()
	dir := b.TempDir()
	for path, content := range map[string]string{
		"go.mod":      implPoolFixture,
		"ifaces/i.go": implPoolIfaces,
		"impls/i.go":  implPoolImpls,
		"main.go":     implPoolMain,
	} {
		p := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	pkgs, err := loadTestPackages(dir)
	if err != nil {
		b.Fatalf("load: %v", err)
	}
	var typePkgs []*types.Package
	for _, p := range pkgs {
		if p.Types != nil {
			typePkgs = append(typePkgs, p.Types)
		}
	}
	pool := newImplTypePool(typePkgs, []string{"example.com/mtest"})
	var store *types.Named
	for _, p := range typePkgs {
		if p.Path() != "example.com/mtest/ifaces" {
			continue
		}
		if tn, ok := p.Scope().Lookup("Store").(*types.TypeName); ok {
			store, _ = tn.Type().(*types.Named)
		}
	}
	if store == nil {
		b.Fatal("Store 接口未找到")
	}
	return pool, store, typePkgs
}

// BenchmarkImplTypePoolCached：二级缓存命中（(接口,方法名) 直接返回）。
func BenchmarkImplTypePoolCached(b *testing.B) {
	pool, store, _ := benchImplPool(b)
	warm := pool.methodsFor(store, "Save")
	if len(warm) == 0 {
		b.Fatal("Store.Save 应有实现")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = pool.methodsFor(store, "Save")
	}
	b.StopTimer()
	b.Logf("computes=%d methodComputes=%d（命中后不应增长）", pool.computes.Load(), pool.methodComputes.Load())
}

// BenchmarkImplTypePoolLegacy：旧实现（每次全库 types.Implements 扫描 + 方法过滤）。
func BenchmarkImplTypePoolLegacy(b *testing.B) {
	_, store, typePkgs := benchImplPool(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = legacyImplMethodsFor(typePkgs, []string{"example.com/mtest"}, store, "Save")
	}
}
