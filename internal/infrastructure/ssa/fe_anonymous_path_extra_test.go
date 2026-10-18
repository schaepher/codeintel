package ssa

// 匿名路径补充回归（最近具名类型 / 轻量提取器）：从
// fe_anonymous_path_test.go 拆出——行数治理。

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"golang.org/x/tools/go/ssa"
)

// TestAnonymousPathNearestNamedType（todo4-1）：匿名嵌套字段的路径以**最近
// 具名结构类型**为起点（Inner，而非外层 Outer）。
func TestAnonymousPathNearestNamedType(t *testing.T) {
	nodes, _ := indexFixture(t, map[string]string{
		"go.mod": moduleGoMod,
		"main.go": `package m

type Inner struct {
	A struct{ B int }
}

type Outer struct {
	In Inner
}

func f(o Outer) {
	_ = o.In.A.B
}
`,
	})
	found := false
	for _, n := range nodes {
		if n.Kind == domain.KindFieldAccess && n.Property("full_path") == "example.com/mtest.Inner.A.B" {
			found = true
		}
	}
	if !found {
		var got []string
		for _, n := range nodes {
			if n.Kind == domain.KindFieldAccess {
				got = append(got, n.Property("full_path"))
			}
		}
		t.Errorf("缺 nearest-named 路径 example.com/mtest.Inner.A.B（实得 %v）", got)
	}
}

// TestSingleStoredValueWithoutExtractorFn（todo4-1）：singleStoredValue 只
// 依赖 alloc.Referrers()，ext.fn 为空（轻量提取器）也应正确追溯。
func TestSingleStoredValueWithoutExtractorFn(t *testing.T) {
	p := testAliasPass(t, map[string]string{
		"go.mod":  moduleGoMod,
		"main.go": "package m\n\nfunc f() *int { x := 1; return &x }\n",
	})
	var alloc *ssa.Alloc
	var stored ssa.Value
	for _, fn := range p.funcs.all() {
		for _, b := range fn.Blocks {
			for _, instr := range b.Instrs {
				st, ok := instr.(*ssa.Store)
				if !ok {
					continue
				}
				if a, ok := st.Addr.(*ssa.Alloc); ok {
					alloc, stored = a, st.Val
				}
			}
		}
	}
	if alloc == nil {
		t.Fatal("fixture 未找到 Alloc 的 Store")
	}
	ext := &fieldExtractor{allocStores: map[*ssa.Alloc]allocStoreResult{}} // fn == nil
	if got := ext.singleStoredValue(alloc); got != stored {
		t.Errorf("singleStoredValue = %v, want %v（不应因 fn 空而返回 nil）", got, stored)
	}
	// 第二次命中缓存，结果一致
	if got := ext.singleStoredValue(alloc); got != stored {
		t.Errorf("缓存命中结果不一致: %v vs %v", got, stored)
	}
}
