package ssa

import (
	"go/token"
	"go/types"
	"testing"
)

// ownerTypesSample 构造一种含嵌套 struct 字段的具名类型（owner 路径展开用）。
func ownerTypesSample(t *testing.T) types.Type {
	t.Helper()
	pkg := types.NewPackage("example.com/m", "m")
	inner := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "Inner", nil),
		types.NewStruct([]*types.Var{
			types.NewField(token.NoPos, pkg, "V", types.Typ[types.Int], false),
		}, nil), nil)
	outer := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "Outer", nil),
		types.NewStruct([]*types.Var{
			types.NewField(token.NoPos, pkg, "In", inner, false),
			types.NewField(token.NoPos, pkg, "P", types.NewPointer(inner), false),
		}, nil), nil)
	return types.NewPointer(outer)
}

// TestOwnerTypesCachedMatchesDirect：缓存结果与纯函数一致（含嵌套 owner）。
func TestOwnerTypesCachedMatchesDirect(t *testing.T) {
	typ := ownerTypesSample(t)
	want := ownerTypesOf(typ, 0)
	if len(want) != 2 || want[0] != "example.com/m.Outer" || want[1] != "example.com/m.Inner" {
		t.Fatalf("ownerTypesOf = %v", want)
	}
	s := &funcSnapshot{}
	got := s.ownerTypesOfCached(typ)
	if len(got) != len(want) {
		t.Fatalf("cached = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("cached[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if again := s.ownerTypesOfCached(typ); len(again) != len(want) {
		t.Fatalf("second hit = %v", again)
	}
}

// TestOwnerTypesCachedFallbackAndIsolation：快照缺失回退纯函数；两快照不共享。
func TestOwnerTypesCachedFallbackAndIsolation(t *testing.T) {
	typ := ownerTypesSample(t)
	ext := &fieldExtractor{}
	if got := ext.ownerTypesOfCached(typ); len(got) != 2 {
		t.Errorf("fallback = %v", got)
	}
	a, b := &funcSnapshot{}, &funcSnapshot{}
	a.ownerTypesOfCached(typ)
	if len(b.owners) != 0 {
		t.Errorf("构建隔离失败：b.owners = %d", len(b.owners))
	}
}

// TestOwnerTypesCachedZeroAllocOnHit：命中路径零分配。
func TestOwnerTypesCachedZeroAllocOnHit(t *testing.T) {
	typ := ownerTypesSample(t)
	s := &funcSnapshot{}
	s.ownerTypesOfCached(typ)
	if n := testing.AllocsPerRun(1000, func() { _ = s.ownerTypesOfCached(typ) }); n != 0 {
		t.Errorf("命中分配 = %v, want 0", n)
	}
}

// BenchmarkOwnerTypesDirect：每次重新递归展开 owner 类型路径。
func BenchmarkOwnerTypesDirect(b *testing.B) {
	pkg := types.NewPackage("example.com/m", "m")
	inner := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "Inner", nil),
		types.NewStruct([]*types.Var{types.NewField(token.NoPos, pkg, "V", types.Typ[types.Int], false)}, nil), nil)
	outer := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "Outer", nil),
		types.NewStruct([]*types.Var{types.NewField(token.NoPos, pkg, "In", inner, false)}, nil), nil)
	typ := types.NewPointer(outer)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = ownerTypesOf(typ, 0)
	}
}

// BenchmarkOwnerTypesCached：命中构建级缓存（零分配）。
func BenchmarkOwnerTypesCached(b *testing.B) {
	pkg := types.NewPackage("example.com/m", "m")
	inner := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "Inner", nil),
		types.NewStruct([]*types.Var{types.NewField(token.NoPos, pkg, "V", types.Typ[types.Int], false)}, nil), nil)
	outer := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "Outer", nil),
		types.NewStruct([]*types.Var{types.NewField(token.NoPos, pkg, "In", inner, false)}, nil), nil)
	typ := types.NewPointer(outer)
	s := &funcSnapshot{}
	s.ownerTypesOfCached(typ)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = s.ownerTypesOfCached(typ)
	}
}
