package cli

// F2 实体领域分组测试：go2o 风格包结构 → DDD 子域分组 + 验证降级。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// go2oStyleGraph 构造 go2o 风格实体图（pkg/domain/<子域> + pkg/infra）。
func go2oStyleGraph() *domain.EntityGraph {
	g := &domain.EntityGraph{}
	n := func(id, pkg string) *domain.EntityNode {
		return &domain.EntityNode{ID: id, Name: id, Pkg: pkg}
	}
	g.Nodes = []*domain.EntityNode{
		n("symbol:go:github.com/ixre/go2o/pkg/domain/order:(Order).Pay", "github.com/ixre/go2o/pkg/domain/order"),
		n("symbol:go:github.com/ixre/go2o/pkg/domain/order:(Order).Cancel", "github.com/ixre/go2o/pkg/domain/order"),
		n("symbol:go:github.com/ixre/go2o/pkg/domain/wallet:(Wallet).Add", "github.com/ixre/go2o/pkg/domain/wallet"),
		n("symbol:go:github.com/ixre/go2o/pkg/domain/member:(Member).Get", "github.com/ixre/go2o/pkg/domain/member"),
		n("symbol:go:github.com/ixre/go2o/pkg/infra:(Repo).Save", "github.com/ixre/go2o/pkg/infra"),
	}
	g.Edges = []*domain.EntityEdge{
		{From: g.Nodes[0].ID, To: g.Nodes[2].ID, Count: 3},
		{From: g.Nodes[0].ID, To: g.Nodes[4].ID, Count: 5},
		{From: g.Nodes[3].ID, To: g.Nodes[4].ID, Count: 2},
	}
	return g
}

// TestSplitEntityDomainsDDD：domain 目录存在 → 子域分组（order/wallet/
// member/infra）。
func TestSplitEntityDomainsDDD(t *testing.T) {
	doms := splitEntityDomains(go2oStyleGraph(), nil)
	if len(doms) != 4 {
		t.Fatalf("领域数 = %d; want 4（order/wallet/member/infra）: %+v", len(doms), doms)
	}
	names := map[string]bool{}
	for _, d := range doms {
		names[d.Name] = true
	}
	for _, want := range []string{"order", "wallet", "member", "infra"} {
		if !names[want] {
			t.Errorf("缺领域 %q: %+v", want, names)
		}
	}
	// 领域内边：order 内无（2 实体无互调）；跨领域边不在领域内
}

// TestSplitEntityDomainsInvalid：结构不规范（全部同段）→ 降级第 2 段。
func TestSplitEntityDomainsInvalid(t *testing.T) {
	g := &domain.EntityGraph{}
	for i := 0; i < 5; i++ {
		g.Nodes = append(g.Nodes, &domain.EntityNode{
			ID:  "symbol:go:example.com/m/pkg" + string(rune('0'+i)) + ":T" + string(rune('0'+i)),
			Pkg: "example.com/m/pkg" + string(rune('0'+i)),
		})
	}
	doms := splitEntityDomains(g, nil)
	// 无 domain/service 段 → 按 module 相对第 1 段（pkg0…pkg4）→ 5 组有效
	if len(doms) < 2 {
		t.Fatalf("无 DDD 目录应降级分段: %+v", doms)
	}
}

// TestSplitEntityDomainsSkewedConfig：R43——有 domains 配置时实体分布
// 偏斜（go2o 实测最大组 >80%）也强制分组（用户要求分领域间/领域内；
// 领域内大图由 500 边降级兜底）——80% 检查仅用于无配置的 DDD 降级。
func TestSplitEntityDomainsSkewedConfig(t *testing.T) {
	g := &domain.EntityGraph{}
	for i := 0; i < 20; i++ { // 20 个 big 域实体（集中）
		g.Nodes = append(g.Nodes, &domain.EntityNode{
			ID:  "symbol:go:example.com/m/big:T" + string(rune('0'+i)),
			Pkg: "example.com/m/big",
		})
	}
	g.Nodes = append(g.Nodes, &domain.EntityNode{ // 1 个小域实体
		ID: "symbol:go:example.com/m/small:T", Pkg: "example.com/m/small",
	})
	doms := splitEntityDomains(g, []wikiDomainCfg{
		{Name: "大域", Packages: []string{"big"}},
		{Name: "小域", Packages: []string{"small"}},
	})
	if len(doms) != 2 {
		t.Fatalf("有配置应强制分组（偏斜也分）: %+v", doms)
	}
	// 无配置（DDD 降级）同样偏斜 → 80% 检查拦截 → 整图一组
	doms2 := splitEntityDomains(g, nil)
	if len(doms2) > 1 {
		t.Fatalf("无配置偏斜应被 80%% 检查拦为整图: %+v", doms2)
	}
}

// TestDomainMermaid：领域间图含节点与聚合边（order→wallet count 3）。
func TestDomainMermaid(t *testing.T) {
	g := go2oStyleGraph()
	doms := splitEntityDomains(g, nil)
	m := domainMermaid(doms, g.Edges)
	if m == "" {
		t.Fatal("领域间图为空")
	}
	// 跨领域边 order→wallet（3）聚合进领域间图
	if !strings.Contains(m, "|3|") {
		t.Errorf("领域间图应含聚合边 order→wallet（3）: %s", m)
	}
	// 领域内边（同领域实体互调）不进领域间图
	if !strings.Contains(m, "|5|") {
		t.Errorf("领域间图应含 order→infra（5）: %s", m)
	}
}
