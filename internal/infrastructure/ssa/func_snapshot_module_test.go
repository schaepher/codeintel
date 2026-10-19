package ssa

import (
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// funcModuleFixture 覆盖：导出/未导出类型方法、提升方法、闭包、函数值、
// 泛型函数实例、方法值包装、MakeInterface（RuntimeTypes）、外部包调用。
const funcModuleFixture = `package m

import "fmt"

type Exported struct{ N int }

func (e Exported) Value() int { return e.N }
func (e *Exported) Ptr()      { e.N++ }

type hidden struct{ M int }

func (h hidden) used() int { return h.M }

type Embeds struct{ Exported }

type Inner[T any] struct{ V T }

func (i *Inner[T]) Save() {}
func (i *Inner[T]) Load() { i.Save() }

type Outer struct{ Inner[int] }

func outerUse(o Outer) int {
	f := o.Load
	f()
	m := (*Inner[int]).Save
	m(&Inner[int]{})
	return 0
}

func helper() int { return 1 }

func generic[T any](v T) T { return v }

func use() int {
	f := helper
	g := func(x int) int { return x + f() }
	_ = fmt.Sprint(g(1))
	var e Exported
	mv := e.Value
	_ = mv()
	_ = generic(3)
	h := hidden{}
	_ = h.used()
	var i interface{ Value() int } = e
	_ = i.Value()
	var emb Embeds
	_ = emb.Value()
	return 0
}
`

// TestModuleFunctionsEqualsFilteredAllFunctions（todo9-8）：模块函数集合与
// ssautil.AllFunctions 的模块子集逐函数一致（含包装/实例/闭包/提升方法）。
func TestModuleFunctionsEqualsFilteredAllFunctions(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), moduleGoMod)
	writeFile(t, filepath.Join(dir, "m.go"), funcModuleFixture)
	pkgs, err := loadTestPackages(dir)
	if err != nil {
		t.Fatal(err)
	}
	prog, _ := ssautil.Packages(pkgs, ssa.BuilderMode(0))
	if prog == nil {
		t.Fatal("ssautil.Packages 失败")
	}
	prog.Build()
	mods := []string{"example.com/mtest"}

	want := map[*ssa.Function]bool{}
	for fn := range ssautil.AllFunctions(prog) {
		if isModuleFunction(fn, mods) {
			want[fn] = true
		}
	}
	got := moduleFunctions(prog, mods)
	gotSet := map[*ssa.Function]bool{}
	for _, fn := range got {
		if gotSet[fn] {
			t.Fatalf("moduleFunctions 重复函数: %s", fn)
		}
		gotSet[fn] = true
	}
	if len(want) != len(gotSet) {
		for fn := range want {
			if !gotSet[fn] {
				t.Errorf("缺函数: %s (pkg=%v)", fn.String(), fn.Pkg)
			}
		}
		for fn := range gotSet {
			if !want[fn] {
				t.Errorf("多函数: %s", fn.String())
			}
		}
		t.Fatalf("模块函数数不一致: got %d, want %d", len(gotSet), len(want))
	}
	for fn := range want {
		if !gotSet[fn] {
			t.Fatalf("缺函数: %s", fn.String())
		}
	}
	// 外部包函数不应进入模块集合
	for fn := range gotSet {
		if fn.Pkg == nil || fn.Pkg.Pkg == nil || !isInModule(fn.Pkg.Pkg.Path(), mods) {
			t.Fatalf("混入非模块函数: %v", fn)
		}
	}

	// 快照路径（生产用）应与集合一致
	snap := newFuncSnapshotForModules(prog, mods)
	if len(snap.all()) != len(gotSet) {
		t.Fatalf("快照函数数不一致: %d vs %d", len(snap.all()), len(gotSet))
	}
	for _, fn := range snap.all() {
		if !gotSet[fn] {
			t.Fatalf("快照含额外函数: %s", fn)
		}
	}
}
