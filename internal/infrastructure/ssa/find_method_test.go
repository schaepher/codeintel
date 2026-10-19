package ssa

import (
	"go/types"
	"testing"
)

func sameFunc(a, b *types.Func) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	return a.String() == b.String()
}

func methodFixture() map[string]string {
	return map[string]string{
		"go.mod": moduleGoMod,
		"main.go": `package m

type Base struct{}

func (b *Base) Promoted() {}

func (b Base) ValueM() {}

type Outer struct{ Base }

func (o *Outer) PtrM() {}

func (o Outer) ValM() {}

func (o Outer) hidden() {}

type Iface interface {
	M()
	Promoted()
}

type Impl struct{ Base }

func (i *Impl) M() {}
`,
	}
}

// TestFindMethodCachedEquivalence（todo8-5）：缓存版与纯 findMethod 逐项
// 一致（值/指针接收者、嵌入提升、不可见方法、不存在；值与指针类型）。
func TestFindMethodCachedEquivalence(t *testing.T) {
	s, _ := testFuncSnapshotFilesAndProg(t, methodFixture())
	names := []string{"PtrM", "ValM", "Promoted", "ValueM", "hidden", "M", "Nope"}
	for _, p := range s.pkgs {
		for _, n := range p.Scope().Names() {
			obj := p.Scope().Lookup(n)
			if obj == nil || obj.Type() == nil {
				continue
			}
			named, ok := obj.Type().(*types.Named)
			if !ok {
				continue
			}
			for _, name := range names {
				if got, want := findMethodCached(s, named, name), findMethod(named, name); !sameFunc(got, want) {
					t.Errorf("%s.%s: cached=%v pure=%v", n, name, got, want)
				}
				ptr := types.NewPointer(named)
				if got, want := findMethodCached(s, ptr, name), findMethod(ptr, name); !sameFunc(got, want) {
					t.Errorf("*%s.%s: cached=%v pure=%v", n, name, got, want)
				}
			}
		}
	}
}

// TestImplPoolMethodsCachedEquivalence（todo8-5）：implTypePool 命中方法集
// 缓存后候选方法与纯 findMethod 的结果/顺序一致（含嵌入提升）。
func TestImplPoolMethodsCachedEquivalence(t *testing.T) {
	s, prog := testFuncSnapshotFilesAndProg(t, methodFixture())
	modules := []string{"example.com/mtest"}
	pool := newImplTypePool(allTypePkgs(prog), modules)
	pool.SetFuncSnapshot(s)
	iface := interfaceNamedOf(s.pkgsByName["m"][0].Scope().Lookup("Iface").Type())
	if iface == nil {
		t.Fatal("fixture 未找到 Iface")
	}
	for _, method := range []string{"M", "Promoted", "Nope"} {
		var want []*types.Func
		for _, pt := range pool.implsFor(iface) {
			if fn := findMethod(pt.named, method); fn != nil {
				want = append(want, fn)
			}
		}
		got := pool.methodsFor(iface, method)
		if len(got) != len(want) {
			t.Fatalf("%s: cached=%d pure=%d", method, len(got), len(want))
		}
		for i := range want {
			if !sameFunc(got[i], want[i]) {
				t.Errorf("%s[%d]: cached=%v pure=%v", method, i, got[i], want[i])
			}
		}
	}
}
