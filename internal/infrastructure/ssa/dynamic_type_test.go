package ssa

import "testing"

// TestDynamicTypeOfCacheEquivalence（todo5-2）：缓存结果与旧扫描逻辑逐项
// 等价（具名/指针/不存在/空输入），重复查询命中缓存；构建隔离。
func TestDynamicTypeOfCacheEquivalence(t *testing.T) {
	s := testFuncSnapshotFiles(t, map[string]string{
		"go.mod":  moduleGoMod,
		"main.go": "package m\n\ntype T struct{ A int }\n\nfunc main() { _ = T{} }\n",
	})
	for _, dyn := range []string{"*m.T", "m.T", "m.Missing", "", "*m.Missing"} {
		want := resolveDynamicType(dyn, s.pkgs)
		got := s.dynamicTypeOf(dyn)
		if (got == nil) != (want == nil) {
			t.Errorf("%q: got %v, want %v", dyn, got, want)
			continue
		}
		if got != nil && got.String() != want.String() {
			t.Errorf("%q: got %q, want %q", dyn, got.String(), want.String())
		}
		again := s.dynamicTypeOf(dyn)
		if (again == nil) != (got == nil) || (again != nil && again.String() != got.String()) {
			t.Errorf("%q: 二次查询不一致: %v vs %v", dyn, again, got)
		}
	}

	// 构建隔离：另一个快照不应看到本快照缓存
	s2 := testFuncSnapshotFiles(t, map[string]string{
		"go.mod":  moduleGoMod,
		"main.go": "package m\n\ntype T struct{ A int }\n",
	})
	if len(s2.dynTypes) != 0 {
		t.Errorf("新快照 dynTypes 应为空，got %d", len(s2.dynTypes))
	}
}

// BenchmarkDynamicTypeIndexBuild 包快照索引成本（newFuncSnapshot）。
func BenchmarkDynamicTypeIndexBuild(b *testing.B) {
	files := map[string]string{"go.mod": moduleGoMod, "main.go": "package m\n\ntype T struct{ A int }\n"}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = benchSnapshot(b, files)
	}
}

// BenchmarkDynamicTypeOfFirst 首次查询（快照已建，清空缓存后扫描一次）。
func BenchmarkDynamicTypeOfFirst(b *testing.B) {
	s := benchSnapshot(b, map[string]string{"go.mod": moduleGoMod, "main.go": "package m\n\ntype T struct{ A int }\n"})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.dynMu.Lock()
		s.dynTypes = nil
		s.dynMu.Unlock()
		_ = s.dynamicTypeOf("*m.T")
	}
}

// BenchmarkDynamicTypeOfCached 命中缓存（零扫描）。
func BenchmarkDynamicTypeOfCached(b *testing.B) {
	s := benchSnapshot(b, map[string]string{"go.mod": moduleGoMod, "main.go": "package m\n\ntype T struct{ A int }\n"})
	s.dynamicTypeOf("*m.T")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.dynamicTypeOf("*m.T")
	}
}
