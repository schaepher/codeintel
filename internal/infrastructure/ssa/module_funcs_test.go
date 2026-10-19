package ssa

import (
	"sync"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func manualModuleFuncs(s *funcSnapshot, modules []string) []*ssa.Function {
	var out []*ssa.Function
	for _, fn := range s.all() {
		if isModuleFunction(fn, modules) {
			out = append(out, fn)
		}
	}
	return out
}

func sameFuncs(a, b []*ssa.Function) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestModuleFuncsCache（todo6-2）：结果与旧过滤逐项一致（身份/顺序），
// 同模块集合复用同一只读切片；不同集合/空集合/无匹配/相似前缀不串味。
func TestModuleFuncsCache(t *testing.T) {
	s := testFuncSnapshotFiles(t, map[string]string{
		"go.mod":         moduleGoMod,
		"main.go":        "package m\n\nfunc F() {}\n",
		"other/other.go": "package other\n\nfunc G() {}\n",
	})
	mods := []string{"example.com/mtest"}
	first := s.moduleFuncs(mods)
	want := manualModuleFuncs(s, mods)
	if !sameFuncs(first, want) {
		t.Fatalf("首次过滤与旧实现不一致: %d vs %d", len(first), len(want))
	}
	second := s.moduleFuncs(mods)
	if !sameFuncs(first, second) {
		t.Fatalf("重复读取结果不一致")
	}
	if len(first) > 0 && &first[0] != &second[0] {
		t.Error("同一模块集合应复用同一只读切片")
	}

	// 空集合
	if got, w := s.moduleFuncs(nil), manualModuleFuncs(s, nil); !sameFuncs(got, w) {
		t.Errorf("空模块集合不一致: %d vs %d", len(got), len(w))
	}
	// 无匹配
	if got, w := s.moduleFuncs([]string{"example.com/nomatch"}), manualModuleFuncs(s, []string{"example.com/nomatch"}); !sameFuncs(got, w) {
		t.Errorf("无匹配不一致: %d vs %d", len(got), len(w))
	}
	// 相似路径前缀（example.com/mtest/other 不应匹配 example.com/mtest）
	if got, w := s.moduleFuncs([]string{"example.com/mtest/other"}), manualModuleFuncs(s, []string{"example.com/mtest/other"}); !sameFuncs(got, w) {
		t.Errorf("相似前缀不一致: %d vs %d", len(got), len(w))
	}
	// 不同模块集合不共用错误结果
	if sameFuncs(s.moduleFuncs([]string{"example.com/mtest/other"}), first) {
		// 允许两者都为空；仅当 first 非空时报错
		if len(first) > 0 {
			t.Error("不同模块集合结果被错误复用")
		}
	}

	// 并发读取（-race）
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if !sameFuncs(s.moduleFuncs(mods), first) {
					t.Errorf("并发读取结果不一致")
					return
				}
			}
		}()
	}
	wg.Wait()
}

// BenchmarkModuleFuncsFirst 首次过滤（清空缓存后）。
func BenchmarkModuleFuncsFirst(b *testing.B) {
	s := benchSnapshot(b, benchCallSiteFixture(100))
	mods := []string{"example.com/mtest"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.moduleMu.Lock()
		s.moduleCache = nil
		s.moduleMu.Unlock()
		_ = s.moduleFuncs(mods)
	}
}

// BenchmarkModuleFuncsCached 命中缓存。
func BenchmarkModuleFuncsCached(b *testing.B) {
	s := benchSnapshot(b, benchCallSiteFixture(100))
	mods := []string{"example.com/mtest"}
	s.moduleFuncs(mods)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.moduleFuncs(mods)
	}
}
