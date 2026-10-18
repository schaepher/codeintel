package ssa

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// anonFieldPaths 收集 fixture 里字段访问节点的 (func_id, instance_path) → full_path。
func anonFieldPaths(t *testing.T) map[string]map[string]string {
	t.Helper()
	nodes, _ := indexFixture(t, map[string]string{
		"go.mod": moduleGoMod,
		"main.go": `package m

type Outer struct {
	Mid struct {
		A int
	}
}

type Other struct {
	Mid struct {
		A int
	}
}

type Basket struct {
	Items []struct {
		Key string
	}
	Arr [2]struct {
		K int
	}
}

type Deep struct {
	Rows []struct {
		Cells []struct {
			V int
		}
	}
}

func val(x Outer, y Other) {
	_ = x.Mid.A
	_ = y.Mid.A
}

func ptr(p *Outer) {
	p.Mid.A = 1
	_ = p.Mid.A
}

func cp(x Outer) {
	m := x.Mid
	_ = m.A
	m.A = 2
}

func idx(b Basket) {
	_ = b.Items[0].Key
	_ = b.Arr[1].K
}

func rng(b Basket) {
	for _, it := range b.Items {
		_ = it.Key
	}
	for _, a := range b.Arr {
		_ = a.K
	}
}

func deep(d Deep) {
	for _, r := range d.Rows {
		for _, c := range r.Cells {
			_ = c.V
		}
	}
}

func sub(d Deep) {
	part := d.Rows[:1]
	_ = part[0].Cells[0].V
}

func standalone() {
	x := struct{ A int }{}
	x.A = 1
}
`,
	})
	out := map[string]map[string]string{}
	for _, n := range nodes {
		if n.Kind != domain.KindFieldAccess {
			continue
		}
		f := n.Property("func_id")
		if out[f] == nil {
			out[f] = map[string]string{}
		}
		out[f][n.Property("instance_path")] = n.Property("full_path")
	}
	return out
}

// TestAnonymousPathTypeDerived：匿名嵌套字段 full_path 由类型派生（不依赖
// 变量名）——同字段不同变量名一致、不同所属类型不混淆。
func TestAnonymousPathTypeDerived(t *testing.T) {
	paths := anonFieldPaths(t)
	val := paths["symbol:go:example.com/mtest:val"]
	if got := val["x.Mid.A"]; got != "example.com/mtest.Outer.Mid.A" {
		t.Errorf("x.Mid.A full_path = %q", got)
	}
	if got := val["y.Mid.A"]; got != "example.com/mtest.Other.Mid.A" {
		t.Errorf("y.Mid.A full_path = %q", got)
	}
}

// TestAnonymousPathPointerAndCopy：指针读写与单一赋值副本都恢复类型路径。
func TestAnonymousPathPointerAndCopy(t *testing.T) {
	paths := anonFieldPaths(t)
	for _, fn := range []string{"ptr", "cp"} {
		got := paths["symbol:go:example.com/mtest:"+fn]
		if len(got) == 0 {
			t.Fatalf("%s 无字段访问节点", fn)
		}
		// 只看叶子 A（中间 p.Mid/x.Mid 的类型路径本就是 Outer.Mid）
		var leaves []string
		for _, fp := range got {
			if strings.HasSuffix(fp, ".Mid.A") {
				leaves = append(leaves, fp)
			}
		}
		if len(leaves) < 1 {
			t.Errorf("%s 缺少 .Mid.A 叶子（实得 %v）", fn, got)
		}
		for _, fp := range leaves {
			if fp != "example.com/mtest.Outer.Mid.A" {
				t.Errorf("%s 叶子 full_path = %q, want Outer.Mid.A", fn, fp)
			}
		}
	}
	// 指针写 + 读各一（access_kind 不同，instance 相同）
	ptr := paths["symbol:go:example.com/mtest:ptr"]
	if ptr["p.Mid.A"] != "example.com/mtest.Outer.Mid.A" {
		t.Errorf("ptr p.Mid.A = %q", ptr["p.Mid.A"])
	}
}

// TestAnonymousPathContainerElements：索引/遍历/数组元素字段带 [*] 层级。
func TestAnonymousPathContainerElements(t *testing.T) {
	paths := anonFieldPaths(t)
	wants := map[string][]string{
		"idx": {"example.com/mtest.Basket.Items[*].Key", "example.com/mtest.Basket.Arr[*].K"},
		"rng": {"example.com/mtest.Basket.Items[*].Key", "example.com/mtest.Basket.Arr[*].K"},
	}
	for fn, want := range wants {
		got := map[string]bool{}
		for _, fp := range paths["symbol:go:example.com/mtest:"+fn] {
			got[fp] = true
		}
		for _, w := range want {
			if !got[w] {
				t.Errorf("%s 缺少 full_path %q（实得 %v）", fn, w, got)
			}
		}
	}
}

// TestAnonymousPathTwoLevelAndSlice：两层匿名元素逐层恢复；子切片保持层级。
func TestAnonymousPathTwoLevelAndSlice(t *testing.T) {
	paths := anonFieldPaths(t)
	const want = "example.com/mtest.Deep.Rows[*].Cells[*].V"
	for _, fn := range []string{"deep", "sub"} {
		found := false
		for _, fp := range paths["symbol:go:example.com/mtest:"+fn] {
			if fp == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s 缺少 %q（实得 %v）", fn, want, paths["symbol:go:example.com/mtest:"+fn])
		}
	}
}

// TestAnonymousPathStandaloneFallback：独立匿名结构体无法恢复所属类型 →
// full_path 回退 instance_path。
func TestAnonymousPathStandaloneFallback(t *testing.T) {
	paths := anonFieldPaths(t)
	got := paths["symbol:go:example.com/mtest:standalone"]
	for inst, fp := range got {
		if fp != inst {
			t.Errorf("独立匿名结构体 %s full_path = %q, want 回退 %q", inst, fp, inst)
		}
	}
}

// TestAnonymousPathDeepNesting（todo3-3）：深层匿名嵌套 + 容器元素链
// （Rows[*].Cells[*].Inner.Leafs[*].V）在提高后的回溯深度上限内完整恢复。
func TestAnonymousPathDeepNesting(t *testing.T) {
	nodes, _ := indexFixture(t, map[string]string{
		"go.mod": moduleGoMod,
		"main.go": `package m

type Deep struct {
	Rows []struct {
		Cells []struct {
			Inner struct {
				Leafs []struct{ V int }
			}
		}
	}
}

func deep(d Deep) {
	for _, r := range d.Rows {
		for _, c := range r.Cells {
			for _, l := range c.Inner.Leafs {
				_ = l.V
			}
		}
	}
}
`,
	})
	const want = "example.com/mtest.Deep.Rows[*].Cells[*].Inner.Leafs[*].V"
	found := false
	for _, n := range nodes {
		if n.Property("full_path") == want {
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
		t.Errorf("缺少深层匿名路径 %q（实得 %v）", want, got)
	}
}

// TestNestedTypePathNilSafe（todo3-3）：空值/空引用不得 panic（保护回退）。
func TestNestedTypePathNilSafe(t *testing.T) {
	ext := &fieldExtractor{}
	if got := ext.nestedTypePath(nil, 0); got != "" {
		t.Errorf("nil 值 typePath = %q, want 空", got)
	}
	if got := ext.singleStoredValue(nil); got != nil {
		t.Errorf("nil Alloc singleStoredValue = %v", got)
	}
	if got := valueScopeTag(nil, "", nil); got != "" {
		t.Errorf("nil 输入作用域后缀 = %q", got)
	}
	if got := ext.typeString(nil); got != "" {
		t.Errorf("nil 类型串 = %q", got)
	}
}
