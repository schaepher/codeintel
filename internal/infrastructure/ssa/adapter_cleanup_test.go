package ssa

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// cleanupFixture 含接口注册 + 动态调用（产生 dispatchPkgs）。
func cleanupFixture() map[string]string {
	return map[string]string{
		"go.mod": moduleGoMod,
		"main.go": `package m

type Iface interface{ M() }

type Impl struct{}

func (i Impl) M() {}

func reg() Iface { return &Impl{} }

func use(i Iface) { i.M() }

func main() { use(reg()) }
`,
	}
}

func newCleanupAdapter(t *testing.T, files map[string]string) (*Adapter, *domain.Repository) {
	t.Helper()
	dir := t.TempDir()
	for p, c := range files {
		writeFile(t, filepath.Join(dir, p), c)
	}
	return &Adapter{}, &domain.Repository{Path: dir, Module: "example.com/mtest", Modules: []string{"example.com/mtest"}}
}

func runCleanupIndex(t *testing.T, a *Adapter, repo *domain.Repository, emit domain.EmitFunc) error {
	t.Helper()
	pkgs, err := loadTestPackages(repo.Path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return a.Index(context.Background(), repo, pkgs, emit)
}

// TestAdapterReleasesTransientAfterIndex（todo5-3）：Index 成功返回后释放
// 函数快照/行缓存/类型映射/派发临时引用；DispatchPkgs 保留。
func TestAdapterReleasesTransientAfterIndex(t *testing.T) {
	a, repo := newCleanupAdapter(t, cleanupFixture())
	if err := runCleanupIndex(t, a, repo, func(domain.Item) error { return nil }); err != nil {
		t.Fatalf("Index: %v", err)
	}
	if a.funcs != nil || a.lines != nil || a.typeMapping != nil || a.dispatchRegs != nil || a.regHits != nil {
		t.Errorf("瞬态引用未释放: funcs=%v lines=%v typeMapping=%v dispatchRegs=%v regHits=%v",
			a.funcs, a.lines, a.typeMapping, a.dispatchRegs, a.regHits)
	}
	if len(a.dispatchPkgs) == 0 {
		t.Error("DispatchPkgs 应保留（Index 返回后 orchestrator 读取）")
	}
	runtime.GC() // 结构性断言为主；此处仅为覆盖强制 GC 路径
}

// TestAdapterReleasesTransientOnError（todo5-3）：emit 错误路径同样释放。
func TestAdapterReleasesTransientOnError(t *testing.T) {
	a, repo := newCleanupAdapter(t, cleanupFixture())
	err := runCleanupIndex(t, a, repo, func(item domain.Item) error {
		if item.Node != nil {
			return errors.New("emit fail")
		}
		return nil
	})
	if err == nil {
		t.Fatal("应返回 emit 错误")
	}
	if a.funcs != nil || a.lines != nil || a.typeMapping != nil || a.dispatchRegs != nil || a.regHits != nil {
		t.Error("错误路径瞬态引用未释放")
	}
}

// TestAdapterReuseAfterIndex（todo5-3）：同一适配器连续两次 Index 产物一致
// （不读取上次构建对象）。
func TestAdapterReuseAfterIndex(t *testing.T) {
	a, repo := newCleanupAdapter(t, cleanupFixture())
	collect := func() []string {
		var ids []string
		if err := runCleanupIndex(t, a, repo, func(item domain.Item) error {
			if item.Node != nil {
				ids = append(ids, string(item.Node.ID))
			}
			return nil
		}); err != nil {
			t.Fatalf("Index: %v", err)
		}
		sort.Strings(ids)
		return ids
	}
	first := collect()
	second := collect()
	if !reflect.DeepEqual(first, second) {
		t.Errorf("两次 Index 产物不一致：%d vs %d", len(first), len(second))
	}
}
