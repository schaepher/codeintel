package orchestrator

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

func node(id, name string, props map[string]any) *domain.CodeEntity {
	return &domain.CodeEntity{
		ID:         domain.CanonicalID(id),
		Kind:       domain.KindFunction,
		Name:       name,
		FilePath:   "a.go",
		LineStart:  1,
		Properties: props,
	}
}

// TestNodeDedupIdentical：同 ID + 同内容连续重复 → 只写第一次。
func TestNodeDedupIdentical(t *testing.T) {
	d := newNodeDedup()
	a := node("symbol:go:m:F", "F", map[string]any{"k": "v", "n": 1})
	if !d.shouldAppend(a) {
		t.Fatal("首次出现应写入")
	}
	if d.shouldAppend(node("symbol:go:m:F", "F", map[string]any{"k": "v", "n": 1})) {
		t.Error("与同 ID 上一次贡献完全相同应跳过")
	}
	if d.shouldAppend(a) {
		t.Error("同指针重复也应跳过")
	}
}

// TestNodeDedupPreservesABA：A→B→A（内容不同）必须全部写入——SQLite
// json_patch 按顺序合并，按"是否出现过"去重会改变最终属性。
func TestNodeDedupPreservesABA(t *testing.T) {
	d := newNodeDedup()
	a1 := node("symbol:go:m:F", "F", map[string]any{"k": "v"})
	b := node("symbol:go:m:F", "F", map[string]any{"k": "v", "extra": "x"})
	a2 := node("symbol:go:m:F", "F", map[string]any{"k": "v"})
	for i, n := range []*domain.CodeEntity{a1, b, a2} {
		if !d.shouldAppend(n) {
			t.Errorf("第 %d 次（A→B→A）应写入", i+1)
		}
	}
}

// TestNodeDedupDistinguishesContent：属性/名称/位置任一不同都视为不同贡献。
func TestNodeDedupDistinguishesContent(t *testing.T) {
	d := newNodeDedup()
	base := node("symbol:go:m:F", "F", map[string]any{"a": 1})
	d.shouldAppend(base)

	diffs := []*domain.CodeEntity{
		node("symbol:go:m:F", "G", map[string]any{"a": 1}),           // Name 不同
		node("symbol:go:m:F", "F", map[string]any{"a": 2}),           // 属性值不同
		node("symbol:go:m:F", "F", map[string]any{"a": 1, "b": "x"}), // 属性键不同
		{ID: "symbol:go:m:F", Kind: domain.KindMethod, Name: "F", FilePath: "a.go", LineStart: 1, Properties: map[string]any{"a": 1}},
	}
	for i, n := range diffs {
		if !d.shouldAppend(n) {
			t.Errorf("差异节点 %d 应写入", i+1)
		}
	}
}

// TestNodeDedupDifferentIDsAndNil：不同 ID 互不影响；nil 直接写入。
func TestNodeDedupDifferentIDsAndNil(t *testing.T) {
	d := newNodeDedup()
	if !d.shouldAppend(node("symbol:go:m:A", "A", nil)) {
		t.Error("A 首次应写入")
	}
	if !d.shouldAppend(node("symbol:go:m:B", "B", nil)) {
		t.Error("B 首次应写入")
	}
	if !d.shouldAppend(nil) {
		t.Error("nil 节点应写入（交给下游处理）")
	}
	var empty *nodeDedup
	if !empty.shouldAppend(node("x", "x", nil)) {
		t.Error("nil 去重表应回退为全部写入")
	}
}

// BenchmarkNodeDedupDuplicate：重复节点场景（每次都与上一次相同 → 全部跳过）。
func BenchmarkNodeDedupDuplicate(b *testing.B) {
	d := newNodeDedup()
	first := node("symbol:go:m:F", "F", map[string]any{"k": "v", "n": 1})
	d.shouldAppend(first)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if d.shouldAppend(node("symbol:go:m:F", "F", map[string]any{"k": "v", "n": 1})) {
			b.Fatal("应跳过")
		}
	}
}

// BenchmarkNodeDedupUnique：唯一节点场景（每次新 ID + 新内容，无跳过收益）。
func BenchmarkNodeDedupUnique(b *testing.B) {
	d := newNodeDedup()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !d.shouldAppend(node("symbol:go:m:F", "F", map[string]any{"i": i})) {
			b.Fatal("应写入")
		}
	}
}
