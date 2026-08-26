package cli

// R33 ER 领域分组测试：表名前缀分领域、领域间边、500 边阈值降级。
// 测试先行。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

func erRels() []*domain.TableRelation {
	return []*domain.TableRelation{
		{FromTable: "mm_member", FromCol: "id", ToTable: "order_tab", ToCol: "member_id", Type: domain.RelationFK},
		{FromTable: "order_tab", FromCol: "id", ToTable: "order_item", ToCol: "order_id", Type: domain.RelationFK},
		{FromTable: "mm_member", FromCol: "id", ToTable: "mch_merchant", ToCol: "owner_id", Type: domain.RelationFK},
		{FromTable: "item_info", FromCol: "id", ToTable: "item_sku", ToCol: "item_id", Type: domain.RelationFK},
		{FromTable: "order_tab", FromCol: "total", ToTable: "mm_member", ToCol: "balance", Type: domain.RelationWrite},
		{FromTable: "secret", FromCol: "x", ToTable: "mm_member", ToCol: "id", Type: domain.RelationQuery},
	}
}

// TestSplitTableDomain：表名前缀（_ 前首段）；无 _ 归 other。
func TestSplitTableDomain(t *testing.T) {
	cases := map[string]string{
		"mm_member":    "mm",
		"order_tab":    "order",
		"orders":       "other",
		"item_info":    "item",
		"mch_merchant": "mch",
	}
	for in, want := range cases {
		if got := splitTableDomain(in); got != want {
			t.Errorf("splitTableDomain(%s) = %s; want %s", in, got, want)
		}
	}
}

// TestSplitERDomains：领域内边分组 + 跨领域边分离；write 类型不计入。
func TestSplitERDomains(t *testing.T) {
	doms, cross := splitERDomains(erRels(), nil, nil)
	byName := map[string]*erDomainGroup{}
	for _, d := range doms {
		byName[d.name] = d
	}
	// 领域内：mm（mm_member 内部无边？——只有跨域；order 组 1 条、item 组 1 条）
	if g := byName["order"]; g == nil || len(g.rels) != 1 || !g.tables["order_tab"] || !g.tables["order_item"] {
		t.Errorf("order 领域 = %+v; want 1 条内部边", g)
	}
	if g := byName["item"]; g == nil || len(g.rels) != 1 {
		t.Errorf("item 领域 = %+v; want 1 条内部边", g)
	}
	// mm 领域无内部边（所有 mm 边都是跨域）——但 mm_member 是关系方
	// 跨域边：mm→order、mm→mch、secret→mm（query 计入）、
	// order→mm（write 不计——splitERDomains 只收 fk/query）
	if len(cross) != 3 {
		t.Errorf("跨领域边 = %d; want 3（mm→order、mm→mch、secret→mm）:\n%+v", len(cross), cross)
	}
}

// TestDiagramEdgeCount：--> 与 ||-- 计数（方案 A 阈值）。
func TestDiagramEdgeCount(t *testing.T) {
	m := "erDiagram\n    a ||--o{ b\n    b -->|2| c\n"
	if n := diagramEdgeCount(m); n != 2 {
		t.Errorf("diagramEdgeCount = %d; want 2", n)
	}
}

// TestDiagramHTMLOverLimit：mermaid 模式超 500 边 → 降级（无 mermaid 块，
// 有提示或 PNG img）。
func TestDiagramHTMLOverLimit(t *testing.T) {
	var b strings.Builder
	b.WriteString("erDiagram\n")
	for i := 0; i < 520; i++ {
		b.WriteString("    t_a ||--o{ t_b\n")
	}
	m := b.String()
	rc := &wikiRenderCtx{Diagram: "mermaid"}
	out := rc.diagramHTML(m)
	if strings.Contains(out, "pre class=\"mermaid\"") {
		t.Error("超限 mermaid 不应输出 mermaid 块（浏览器挂）")
	}
	if !strings.Contains(out, "图过大") && !strings.Contains(out, "data:image/png") {
		t.Errorf("超限应降级（提示或 PNG）:\n%.100s", out)
	}
	// 500 边内正常输出
	rc2 := &wikiRenderCtx{Diagram: "mermaid"}
	small := "erDiagram\n    a ||--o{ b\n"
	if !strings.Contains(rc2.diagramHTML(small), "pre class=\"mermaid\"") {
		t.Error("500 边内应正常输出 mermaid 块")
	}
}
