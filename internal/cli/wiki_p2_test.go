package cli

// R100 待办14（P2 候选五项）：
// ② 服务归属静态兜底（服务名前缀 → 表前缀域匹配——无投票命中时）
// ④ ER 500 边细分——子域内仍超限的递归保护（提示 + 表级统计，不渲染
//    超限图）

import (
	"fmt"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
)

// TestServiceStaticDomain：R100 待办14-②——无投票命中（无 services/
// packages 匹配）时，服务名前缀 → 表前缀域匹配（ItemService → item →
// 商品域 tables 前缀）。
func TestServiceStaticDomain(t *testing.T) {
	dir := seedRepo(t)
	acts, err := newTestActions(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	rc := &wikiRenderCtx{acts: acts, cfg: wikiConfig{Domains: []wikiDomainCfg{
		{Name: "商品", Tables: []string{"item_order", "item_sku"}},
		{Name: "订单", Tables: []string{"order_main"}},
	}}}
	svc := action.GrpcRouteService{Name: "ItemService"}
	if got := serviceDomain(rc, svc); got != "商品" {
		t.Errorf("ItemService 静态兜底 = %q; want 商品（表前缀匹配）", got)
	}
	// 精确表名前缀但服务名无对应 → 空（走「其他」）
	svc2 := action.GrpcRouteService{Name: "UnknownSvc"}
	if got := serviceDomain(rc, svc2); got != "" {
		t.Errorf("UnknownSvc = %q; want 空", got)
	}
	// 显式 services 配置优先（静态兜底不覆盖显式配置）
	rc2 := &wikiRenderCtx{acts: acts, cfg: wikiConfig{Domains: []wikiDomainCfg{
		{Name: "支付", Services: []string{"ItemService"}},
		{Name: "商品", Tables: []string{"item_order"}},
	}}}
	if got := serviceDomain(rc2, svc); got != "支付" {
		t.Errorf("显式 services 应优先 = %q; want 支付", got)
	}
}

// TestRenderValueLinks：R100 待办14-①渲染端——数据流串联标注展示
// （入口写入 → 下游读取）。
func TestRenderValueLinks(t *testing.T) {
	dir := seedRepo(t)
	acts, err := newTestActions(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	chain := &action.ProcChain{Entry: "main",
		Steps: []domain.WikiSeqStep{{Caller: "main", Callee: "(Svc).Run"}},
		ValueLinks: []action.ValueLink{
			{Field: "example.com/m.T.A", ProducedBy: "main", ReadBy: "(Svc).Run"}}}
	rc := &wikiRenderCtx{acts: acts, Diagram: "mermaid"}
	md := renderProcChainMD(rc, chain, "")
	if !strings.Contains(md, "数据流串联") {
		t.Errorf("应展示数据流串联区块:\n%s", md)
	}
	if !strings.Contains(md, "main") || !strings.Contains(md, "T.A") || !strings.Contains(md, "(Svc).Run") {
		t.Errorf("串联标注应含 入口/字段/被调者:\n%s", md)
	}
	html := renderProcChainHTML(rc, chain, "")
	if !strings.Contains(html, "数据流串联") {
		t.Errorf("html 版应展示数据流串联区块:\n%s", html)
	}
}

// TestERSubDomainOverLimit：R100 待办14-④——子域内关系仍 >500 时不再
// 渲染超限图（提示 + 表级统计），小子域正常渲染。
func TestERSubDomainOverLimit(t *testing.T) {
	// 30 表全连 = 435 边（<500）；42 表全连 = 861 边（>500）
	big := &erDomainGroup{name: "big", tables: map[string]bool{}, rels: []*domain.TableRelation{}}
	for i := 0; i < 42; i++ {
		for j := i + 1; j < 42; j++ {
			big.rels = append(big.rels, &domain.TableRelation{
				FromTable: fmt.Sprintf("item_%02d", i), ToTable: fmt.Sprintf("item_%02d", j),
				Type: domain.RelationQuery})
			big.tables[fmt.Sprintf("item_%02d", i)] = true
			big.tables[fmt.Sprintf("item_%02d", j)] = true
		}
	}
	rc := &wikiRenderCtx{Diagram: "mermaid", cfg: wikiConfig{Domains: []wikiDomainCfg{
		{Name: "商品", Subdomains: []action.WikiSubdomainCfg{{Name: "big", Tables: []string{"item_00"}}}}}}}
	md := renderERSubDomainsMD(big, rc)
	if strings.Contains(md, "||--") {
		t.Errorf("超限子域不应渲染图（mermaid 边仍存在）:\n%.400s", md)
	}
	if !strings.Contains(md, "仍超上限") {
		t.Errorf("超限子域应有提示:\n%.400s", md)
	}
	if !strings.Contains(md, "最热表对") {
		t.Errorf("超限子域应含表级统计:\n%.400s", md)
	}
	// 小子域（10 表全连 45 边）正常渲染图
	small := &erDomainGroup{name: "small", tables: map[string]bool{}, rels: []*domain.TableRelation{}}
	for i := 0; i < 10; i++ {
		for j := i + 1; j < 10; j++ {
			small.rels = append(small.rels, &domain.TableRelation{
				FromTable: fmt.Sprintf("item_%02d", i), ToTable: fmt.Sprintf("item_%02d", j),
				Type: domain.RelationQuery})
		}
	}
	md2 := renderERSubDomainsMD(small, rc)
	if !strings.Contains(md2, "||--") {
		t.Errorf("小子域应渲染图（erDiagram 边）:\n%.400s", md2)
	}
}
