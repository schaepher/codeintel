package ssa

import (
	"go/types"
	"path/filepath"
	"sort"
	"sync"
	"testing"
)

// 遗留实现（Q246 改造前的 implMethodsFor）——保留在测试里作 golden 对照：
// 池化/memo 必须与「每次调用重扫全部包 scope」的结果逐条一致。
func legacyImplMethodsFor(pkgs []*types.Package, modules []string, iface *types.Named, method string) []*types.Func {
	var out []*types.Func
	for _, pkg := range pkgs {
		if !isInModule(pkg.Path(), modules) {
			continue
		}
		scope := pkg.Scope()
		for _, name := range scope.Names() {
			tn, ok := scope.Lookup(name).(*types.TypeName)
			if !ok {
				continue
			}
			named, ok := tn.Type().(*types.Named)
			if !ok {
				continue
			}
			if named == iface {
				continue
			}
			if !types.Implements(named, iface.Underlying().(*types.Interface)) &&
				!types.Implements(types.NewPointer(named), iface.Underlying().(*types.Interface)) {
				continue
			}
			if fn := findMethod(named, method); fn != nil {
				out = append(out, fn)
			}
		}
	}
	return out
}

// implPoolFixture 两包 fixture：接口在 ifaces 包，实现者在 impls 包
// （值实现/指针实现/不实现/接口自反四种形态），另有依赖包（非模块内，
// 池必须跳过）。
const implPoolFixture = `module example.com/mtest

go 1.26
`

const implPoolIfaces = `package ifaces

type Store interface {
	Save(v string) error
	Load(k string) (string, error)
}

type Closer interface {
	Close() error
}
`

const implPoolImpls = `package impls

// ValueStore 值实现（Save/Load 值方法集）
type ValueStore struct{ n int }

func (v ValueStore) Save(val string) error       { return nil }
func (v ValueStore) Load(k string) (string, error) { return "", nil }

// PtrStore 指针实现（指针方法集）
type PtrStore struct{ n int }

func (p *PtrStore) Save(val string) error           { return nil }
func (p *PtrStore) Load(k string) (string, error)   { return "", nil }
func (p *PtrStore) Close() error                    { return nil }

// Noop 不实现任何接口
type Noop struct{}

func (n Noop) Save() {}
`

const implPoolMain = `package main

import (
	"example.com/mtest/ifaces"
	"example.com/mtest/impls"
)

func main() {
	var s ifaces.Store = impls.ValueStore{}
	_ = s
	var c ifaces.Closer = &impls.PtrStore{}
	_ = c
}
`

func implPoolTestPool(t *testing.T) (*implTypePool, []*types.Package) {
	t.Helper()
	dir := t.TempDir()
	for path, content := range map[string]string{
		"go.mod":      implPoolFixture,
		"ifaces/i.go": implPoolIfaces,
		"impls/i.go":  implPoolImpls,
		"main.go":     implPoolMain,
	} {
		writeFile(t, filepath.Join(dir, path), content)
	}
	pkgs, err := loadTestPackages(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var typePkgs []*types.Package
	for _, p := range pkgs {
		if p.Types != nil {
			typePkgs = append(typePkgs, p.Types)
		}
	}
	modules := []string{"example.com/mtest"}
	return newImplTypePool(typePkgs, modules), typePkgs
}

// ifaceNamed 从包集合中按包路径/类型名取接口类型。
func ifaceNamed(t *testing.T, pkgs []*types.Package, pkgPath, name string) *types.Named {
	t.Helper()
	for _, p := range pkgs {
		if p.Path() != pkgPath {
			continue
		}
		if tn, ok := p.Scope().Lookup(name).(*types.TypeName); ok {
			if named, ok := tn.Type().(*types.Named); ok {
				return named
			}
		}
	}
	t.Fatalf("iface not found: %s.%s", pkgPath, name)
	return nil
}

func funcNames(fns []*types.Func) []string {
	out := make([]string, 0, len(fns))
	for _, fn := range fns {
		recv := ""
		if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
			recv = "(" + sig.Recv().Type().String() + ")."
		}
		out = append(out, fn.Pkg().Path()+"."+recv+fn.Name())
	}
	sort.Strings(out)
	return out
}

// TestImplTypePoolMatchesLegacyScan：池化 + memo 的结果必须与旧的
// 「每次重扫全部包 scope」实现逐条一致（含值方法集/指针方法集、接口自反
// 排除、非模块内包跳过）：ValueStore 值实现、PtrStore 指针实现（Save/
// Load/Close 各形态），Noop.Save 签名不符不入候选。
func TestImplTypePoolMatchesLegacyScan(t *testing.T) {
	pool, typePkgs := implPoolTestPool(t)
	modules := []string{"example.com/mtest"}
	store := ifaceNamed(t, typePkgs, "example.com/mtest/ifaces", "Store")
	closer := ifaceNamed(t, typePkgs, "example.com/mtest/ifaces", "Closer")

	for _, tc := range []struct {
		name      string
		iface     *types.Named
		method    string
		wantEmpty bool // 该形态本就无候选（防「用例失效」误报）
	}{
		{"Store.Save", store, "Save", false},
		{"Store.Load", store, "Load", false},
		{"Closer.Close", closer, "Close", false},
		{"Store.Missing", store, "Missing", true},
	} {
		got := funcNames(pool.methodsFor(tc.iface, tc.method))
		want := funcNames(legacyImplMethodsFor(typePkgs, modules, tc.iface, tc.method))
		if len(want) == 0 && !tc.wantEmpty {
			t.Errorf("%s: fixture 未命中任何实现（用例失效）", tc.name)
		}
		if len(got) != len(want) {
			t.Fatalf("%s: got %v, want %v", tc.name, got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("%s[%d]: got %s, want %s", tc.name, i, got[i], want[i])
			}
		}
	}
}

// TestImplTypePoolHitAndConcurrency：同键重复调用返回同一结果（memo
// 生效、返回切片只读复用），并发调用无竞态（-race 基线）。
func TestImplTypePoolHitAndConcurrency(t *testing.T) {
	pool, typePkgs := implPoolTestPool(t)
	store := ifaceNamed(t, typePkgs, "example.com/mtest/ifaces", "Store")

	first := pool.methodsFor(store, "Save")
	second := pool.methodsFor(store, "Save")
	if len(first) == 0 {
		t.Fatal("Store.Save 应命中实现")
	}
	if &first[0] != &second[0] {
		t.Errorf("同键应返回同一底层数组（memo 未生效）")
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				_ = pool.methodsFor(store, "Save")
			} else {
				_ = pool.methodsFor(store, "Load")
			}
		}(i)
	}
	wg.Wait()
}

// TestImplTypePoolSkipsNonModule：非模块内包的类型不入池（旧实现按
// isInModule 过滤——池构建期过滤，语义一致）。
func TestImplTypePoolSkipsNonModule(t *testing.T) {
	dir := t.TempDir()
	for path, content := range map[string]string{
		"go.mod":      implPoolFixture,
		"ifaces/i.go": implPoolIfaces,
		"impls/i.go":  implPoolImpls,
	} {
		writeFile(t, filepath.Join(dir, path), content)
	}
	pkgs, err := loadTestPackages(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var typePkgs []*types.Package
	for _, p := range pkgs {
		if p.Types != nil {
			typePkgs = append(typePkgs, p.Types)
		}
	}
	// 只把 impls 声明为模块内 → 池不应含 ifaces.Store
	pool := newImplTypePool(typePkgs, []string{"example.com/mtest/impls"})
	store := ifaceNamed(t, typePkgs, "example.com/mtest/ifaces", "Store")
	for _, pt := range pool.types {
		if pt.named == store {
			t.Fatal("非模块内包的类型不应入池")
		}
	}
}
