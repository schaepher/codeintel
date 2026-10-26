package ssa

// P1-04：instancePath / recoverVarName / allocTypeShort 构建期缓存回归。
//
// 约束：缓存只影响"重复付出"，不得改变任何输出——每个值都必须与
// 未缓存的原始实现（instancePathDepth / recoverVarNameUncached /
// allocTypeShort 纯函数）逐字一致；并发首次访问安全（-race）。

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"

	"github.com/schaepher/codeintel/internal/domain"
)

// pathCacheFixture 覆盖实例路径的典型形态：局部变量读写、匿名分配
// （&T{}，触发 allocTypeShort 回退）、函数返回值提升（recoverVarName）、
// 嵌套字段与切片元素。
const pathCacheFixture = `package m

type Inner struct {
	A int
	B string
}

type Outer struct {
	In   Inner
	List []Inner
}

func newOuter() Outer { return Outer{} }

func f(x *Outer, n int) int {
	y := newOuter()
	y.In.A = n
	o := &Outer{}
	o.In.B = y.In.B
	z := o.List[0]
	return z.A + x.In.A
}
`

// testPathCacheExtractor 构造真实提取器（idents/assignTargets/快照齐备）。
func testPathCacheExtractor(t *testing.T) (*fieldExtractor, []*ssa.Function) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), strings.TrimSpace(moduleGoMod)+"\n")
	writeFile(t, filepath.Join(dir, "main.go"), pathCacheFixture)
	pkgs, err := loadTestPackages(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	prog, _ := ssautil.Packages(pkgs, ssa.BuilderMode(0))
	if prog == nil {
		t.Fatal("ssautil.Packages 失败")
	}
	prog.Build()
	modules := []string{"example.com/mtest"}
	ext := &fieldExtractor{
		repo:          &domain.Repository{Path: dir},
		prog:          prog,
		idents:        buildIdentIndex(pkgs, modules),
		assignTargets: buildAssignTargets(pkgs, modules),
		funcs:         newFuncSnapshot(prog),
		funcIDs:       map[*ssa.Function]domain.CanonicalID{},
	}
	var fns []*ssa.Function
	for _, fn := range ext.funcs.all() {
		if strings.HasPrefix(fn.String(), "example.com/mtest.f") {
			fns = append(fns, fn)
		}
	}
	if len(fns) != 1 {
		t.Fatalf("应找到 fixture 函数 f，得到 %d 个", len(fns))
	}
	return ext, fns
}

// ssaValuesOf 收集函数内全部 SSA 值（含指令结果与操作数）。
func ssaValuesOf(fn *ssa.Function) []ssa.Value {
	seen := map[ssa.Value]bool{}
	var out []ssa.Value
	add := func(v ssa.Value) {
		if v == nil || seen[v] {
			return
		}
		seen[v] = true
		out = append(out, v)
	}
	add(fn)
	for _, p := range fn.Params {
		add(p)
	}
	for _, b := range fn.Blocks {
		for _, instr := range b.Instrs {
			if v, ok := instr.(ssa.Value); ok {
				add(v)
			}
			for _, op := range instr.Operands(nil) {
				if op != nil {
					add(*op)
				}
			}
		}
	}
	return out
}

// TestInstancePathMemoEqualsUncached 缓存命中结果必须与原始递归实现逐字
// 一致（同一值重复调用稳定，且缓存确实被填充）。
func TestInstancePathMemoEqualsUncached(t *testing.T) {
	ext, fns := testPathCacheExtractor(t)
	values := ssaValuesOf(fns[0])
	if len(values) == 0 {
		t.Fatal("fixture 未取到 SSA 值")
	}
	for _, v := range values {
		want := ext.instancePathDepth(v, 0)
		got := ext.instancePath(v)
		if got != want {
			t.Errorf("instancePath(%s/%T) = %q，未缓存实现为 %q", v.Name(), v, got, want)
		}
		if cached, ok := ext.paths[v]; !ok || cached != got {
			t.Errorf("%s 未按值缓存（ok=%v cached=%q）", v.Name(), ok, cached)
		}
		if again := ext.instancePath(v); again != got {
			t.Errorf("第二次调用不一致: %q vs %q", again, got)
		}
	}
	// 第二轮命中不新增条目（缓存只增不改、键集合稳定）
	before := len(ext.paths)
	for _, v := range values {
		_ = ext.instancePath(v)
	}
	if after := len(ext.paths); after != before {
		t.Errorf("缓存命中不应新增条目：%d → %d", before, after)
	}
}

// TestRecoverVarNameMemoEqualsUncached 变量名恢复缓存等价性。
func TestRecoverVarNameMemoEqualsUncached(t *testing.T) {
	ext, fns := testPathCacheExtractor(t)
	var recovered int
	for _, v := range ssaValuesOf(fns[0]) {
		want := ext.recoverVarNameUncached(v)
		if got := ext.recoverVarName(v); got != want {
			t.Errorf("recoverVarName(%s/%T) = %q，未缓存实现为 %q", v.Name(), v, got, want)
		}
		if got := ext.recoverVarName(v); got != want {
			t.Errorf("第二次调用不一致: %q vs %q", got, want)
		}
		if !isSSAName(v.Name()) && v.Name() != "" {
			continue
		}
		// Alloc（tN）应恢复出源码变量名（y/o/z）或至少稳定非空
		if want != "" && !isSSAName(want) {
			recovered++
		}
	}
	if recovered == 0 {
		t.Error("fixture 应至少恢复出一个源码变量名（否则用例未覆盖 recoverVarName）")
	}
	if len(ext.varNames) == 0 {
		t.Error("变量名缓存未被填充")
	}
}

// TestAllocTypeShortCache 构建级类型短名缓存：与纯函数一致、空串不缓存、
// 快照缺失回退纯函数、并发首次访问安全（-race）。
func TestAllocTypeShortCache(t *testing.T) {
	ext, _ := testPathCacheExtractor(t)
	s := ext.funcs
	types := []string{
		"",
		"*github.com/ixre/go2o/pkg/infra/domain.DomainError",
		"[]*example.com/mtest.Outer",
		"*string",
		"int",
		"map[string]github.com/x/y.Z",
		"*interface{}",
	}
	for _, ts := range types {
		want := allocTypeShort(ts)
		if got := s.allocTypeShort(ts); got != want {
			t.Errorf("allocTypeShort(%q) = %q，纯函数为 %q", ts, got, want)
		}
		if got := s.allocTypeShort(ts); got != want {
			t.Errorf("第二次调用不一致: %q vs %q", got, want)
		}
		if got := ext.allocTypeShort(ts); got != want {
			t.Errorf("提取器侧 allocTypeShort(%q) = %q，want %q", ts, got, want)
		}
	}
	if _, ok := s.shorts[""]; ok {
		t.Error("空串不应进缓存（无复用价值）")
	}
	// 快照缺失（轻量提取器）回退纯函数，不静默返回空串
	var empty fieldExtractor
	if got := empty.allocTypeShort("*example.com/m.T"); got != allocTypeShort("*example.com/m.T") || got == "" {
		t.Errorf("快照缺失应回退纯函数，得到 %q", got)
	}
	var nilSnap *funcSnapshot
	if got := nilSnap.allocTypeShort("[]int"); got != "[]int" {
		t.Errorf("nil 快照应回退纯函数，得到 %q", got)
	}
	// 并发首次访问
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				for _, ts := range types {
					if got, want := s.allocTypeShort(ts), allocTypeShort(ts); got != want {
						t.Errorf("并发结果不一致: %q vs %q", got, want)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
}
