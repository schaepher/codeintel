package ssa

import (
	"go/types"
	"testing"
)

func typePath(t interface{ String() string }) string { return t.String() }

// pkgPathOfType 取类型（含指针）所属包的路径。
func pkgPathOfType(t types.Type) string {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	if n, ok := t.(*types.Named); ok && n.Obj() != nil && n.Obj().Pkg() != nil {
		return n.Obj().Pkg().Path()
	}
	return ""
}

// TestDynamicTypeIndexedEquivalence（todo6-1）：按包名/路径索引收窄后的
// 解析与旧全量扫描逐项等价（覆盖具名/指针/别名/不存在）。
func TestDynamicTypeIndexedEquivalence(t *testing.T) {
	s := testFuncSnapshotFiles(t, map[string]string{
		"go.mod": moduleGoMod,
		"main.go": `package m

type T struct{ A int }
type Alias = T
type Ptr *T
`,
	})
	seen := map[string]bool{}
	for _, p := range s.pkgs {
		for _, name := range p.Scope().Names() {
			obj := p.Scope().Lookup(name)
			if obj == nil || obj.Type() == nil {
				continue
			}
			for _, dyn := range []string{obj.Type().String(), "*" + obj.Type().String()} {
				if seen[dyn] {
					continue
				}
				seen[dyn] = true
				want := resolveDynamicType(dyn, s.pkgs)
				got := s.dynamicTypeOf(dyn)
				if (got == nil) != (want == nil) {
					t.Errorf("%q: got %v, want %v", dyn, got, want)
					continue
				}
				if got != nil && typePath(got) != typePath(want) {
					t.Errorf("%q: got %q, want %q", dyn, got, want)
				}
			}
		}
	}
	// 空/不存在
	for _, dyn := range []string{"", "m.Missing", "*m.Missing"} {
		want := resolveDynamicType(dyn, s.pkgs)
		got := s.dynamicTypeOf(dyn)
		if (got == nil) != (want == nil) {
			t.Errorf("%q: got %v, want %v", dyn, got, want)
		}
	}
}

// TestDynamicTypeIndexedSamePackageName（todo6-1）：不同路径、同名包的
// 类型串收窄后仍保持原候选顺序（按路径排序，取第一个）。
func TestDynamicTypeIndexedSamePackageName(t *testing.T) {
	s := testFuncSnapshotFiles(t, map[string]string{
		"go.mod": moduleGoMod,
		"a/a.go": "package m\n\ntype T struct{ A int }\n",
		"b/b.go": "package m\n\ntype T struct{ B int }\n",
	})
	// 取 a 包 T 的实际类型串（Go 1.26 Type.String() 限定符为包路径）
	aPkgs := s.pkgsByName["m"]
	if len(aPkgs) < 2 {
		t.Fatalf("同名包快照 = %d, want 2", len(aPkgs))
	}
	dyn := "*" + aPkgs[0].Scope().Lookup("T").Type().String()
	want := resolveDynamicType(dyn, s.pkgs)
	got := s.dynamicTypeOf(dyn)
	if want == nil || got == nil {
		t.Fatalf("同名包解析失败: want=%v got=%v", want, got)
	}
	// 两侧应选同一个包（排序后路径最小者 a）
	if pkgPathOfType(got) != pkgPathOfType(want) {
		t.Errorf("候选顺序不一致: got %s, want %s", pkgPathOfType(got), pkgPathOfType(want))
	}
	if pkgPathOfType(got) != "example.com/mtest/a" {
		t.Errorf("应取路径最小包 a，got %s", pkgPathOfType(got))
	}
}
