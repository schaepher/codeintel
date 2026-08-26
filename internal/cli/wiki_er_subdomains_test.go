package cli

// R78 测试：ER 域内图超限子域细分——splitERSubDomains 分组 + 渲染
// 输出（子域间图 + 子域内折叠）。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// erTestRels 构造关系集：item_order_* 三表链 + item_sku 独立对 +
// member_* 两表 + 跨子域（item_order → member）。
func erTestRels() []*domain.TableRelation {
	mk := func(from, fcol, to, tcol string) *domain.TableRelation {
		return &domain.TableRelation{FromTable: from, FromCol: fcol, ToTable: to, ToCol: tcol, Type: domain.RelationFK}
	}
	return []*domain.TableRelation{
		mk("item_order_a", "id", "item_order_b", "order_id"),
		mk("item_order_b", "id", "item_order_c", "b_id"),
		mk("item_sku", "id", "item_order_a", "sku_id"),
		mk("member_a", "id", "member_b", "m_id"),
		mk("item_order_c", "id", "member_a", "owner_id"), // 跨子域
	}
}

// TestERSubName：二级前缀提取（两段/一段/无 _）。
func TestERSubName(t *testing.T) {
	cases := map[string]string{
		"item_order_detail": "item_order", // ≥3 段 → 二级前缀
		"item_order_a":      "item_order",
		"member_a":          "member", // 2 段 → 一级前缀
		"plain":             "other",  // 无 _ → 一级前缀缺省 other
	}
	for in, want := range cases {
		if got := erSubName(in); got != want {
			t.Errorf("erSubName(%q) = %q; want %q", in, got, want)
		}
	}
}

// TestSplitERSubDomains：组内/跨组划分 + 确定性排序。边两端二级前缀
// 相同才成组——item_sku 只有跨组边（sku→item_order）不成组。
func TestSplitERSubDomains(t *testing.T) {
	subs, cross := splitERSubDomains(erTestRels(), nil)
	names := []string{}
	for _, s := range subs {
		names = append(names, s.name)
	}
	if len(names) != 2 || names[0] != "item_order" || names[1] != "member" {
		t.Errorf("子域分组 = %v; want [item_order member]", names)
	}
	for _, s := range subs {
		switch s.name {
		case "item_order":
			if len(s.rels) != 2 {
				t.Errorf("item_order 组内边 = %d; want 2（a→b、b→c）", len(s.rels))
			}
		case "member":
			if len(s.rels) != 1 {
				t.Errorf("member 组内边 = %d; want 1", len(s.rels))
			}
		}
	}
	if len(cross) != 2 {
		t.Errorf("跨子域边 = %d; want 2（sku→item_order_a、item_order_c→member_a）", len(cross))
	}
}

// TestRenderERSubDomainsMD：子域分组说明 + 子域 details。
func TestRenderERSubDomainsMD(t *testing.T) {
	d := &erDomainGroup{name: "item", rels: erTestRels(), tables: map[string]bool{}}
	for _, r := range erTestRels() {
		d.tables[r.FromTable] = true
		d.tables[r.ToTable] = true
	}
	rc := &wikiRenderCtx{Diagram: "mermaid"}
	out := renderERSubDomainsMD(d, rc)
	for _, want := range []string{"子域分组", "item_order", "member"} {
		if !strings.Contains(out, want) {
			t.Errorf("子域渲染应含 %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "图过大") {
		t.Errorf("子域细分后不应出现图过大提示:\n%s", out)
	}
}

// TestSplitERSubDomainsYaml：R80——yaml subdomains.tables 优先于二级
// 前缀（member_a/member_b 显式归「会员资料」子域；item 表自动降级）。
func TestSplitERSubDomainsYaml(t *testing.T) {
	subTables := map[string]string{"member_a": "会员资料", "member_b": "会员资料"}
	subs, cross := splitERSubDomains(erTestRels(), subTables)
	names := []string{}
	for _, s := range subs {
		names = append(names, s.name)
	}
	// member_a/member_b 全被 yaml 归「会员资料」（无 member 表剩下——
	// 覆盖即意图）；item_* 自动二级前缀
	if len(names) != 2 || names[0] != "item_order" || names[1] != "会员资料" {
		t.Errorf("yaml 优先子域分组 = %v; want [item_order 会员资料]", names)
	}
	for _, s := range subs {
		if s.name == "会员资料" && len(s.rels) != 1 {
			t.Errorf("会员资料组内边 = %d; want 1（member_a→member_b）", len(s.rels))
		}
	}
	if len(cross) != 2 {
		t.Errorf("跨子域边 = %d; want 2", len(cross))
	}
}
