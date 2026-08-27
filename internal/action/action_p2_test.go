package action

// R100 待办14（P2 候选五项）：
// ① 流程页深度——value-trace 串联（入口写入字段 → 下游被调者读取交集）
// ⑤ 实体对 Top-N（DomainFacts top_pairs——实体对级调用热度）

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedValueLinkRepo：main 写 T.A；(Svc).Run 读 T.A——入口写入 →
// 下游读取的 value-trace 串联场景（seedRepo 已有 main → (Svc).Run 的
// calls 边）。
func seedValueLinkRepo(t *testing.T) *Actions {
	t.Helper()
	a, dir := seedRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats(nil, nil, []*domain.FunctionFieldSummary{
		{FunctionID: "symbol:go:example.com/m:main", AccessKind: domain.SummaryDirectWrite,
			FieldPath: "example.com/m.T.A", InstancePath: "t.A", LineStart: 6},
		{FunctionID: "symbol:go:example.com/m/svc:(Svc).Run", AccessKind: domain.SummaryDirectRead,
			FieldPath: "example.com/m.T.A", InstancePath: "t.A", LineStart: 7},
	}); err != nil {
		t.Fatal(err)
	}
	return a
}

// TestQueryChainValueLinks：R100 待办14-①——入口写入字段与下游被调者
// 读取的交集自动标注（跨符号连接——不再只列每符号读写清单）。
func TestQueryChainValueLinks(t *testing.T) {
	a := seedValueLinkRepo(t)
	chain := a.QueryChain("main")
	if chain == nil || len(chain.Steps) == 0 {
		t.Fatalf("QueryChain 应有调用链: %+v", chain)
	}
	if len(chain.ValueLinks) != 1 {
		t.Fatalf("ValueLinks = %+v; want 1 条（main 写 T.A → (Svc).Run 读）", chain.ValueLinks)
	}
	v := chain.ValueLinks[0]
	if v.Field != "example.com/m.T.A" || v.ProducedBy != "main" || !strings.Contains(v.ReadBy, "Run") {
		t.Errorf("ValueLink = %+v; want Field=T.A ProducedBy=main ReadBy=(Svc).Run", v)
	}
	// 反向（读 → 写）不标注——只标注入口写入方向
	if len(chain.KeyFlows) == 0 {
		t.Error("KeyFlows 应有数据（串联的数据源）")
	}
}

// TestDomainFactsTopPairs：R100 待办14-⑤——实体对热度 Top-N（按 Count
// 降序截断；域分析 prompt 能看到最热调用对）。实体图 = 有方法的类型 +
// has_method/calls 边。
func TestDomainFactsTopPairs(t *testing.T) {
	a, dir := seedRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:Order", Kind: domain.KindStruct, Name: "Order"},
		{ID: "symbol:go:example.com/m:(Order).Save", Kind: domain.KindMethod, Name: "(Order).Save"},
		{ID: "symbol:go:example.com/m/svc:Svc", Kind: domain.KindStruct, Name: "Svc"},
		{ID: "symbol:go:example.com/m/svc:(Svc).Run", Kind: domain.KindMethod, Name: "(Svc).Run"},
		{ID: "symbol:go:example.com/m/svc:(Svc).Load", Kind: domain.KindMethod, Name: "(Svc).Load"},
	}
	if _, err := r.SaveBatchStats(nodes, []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:Order", TargetID: "symbol:go:example.com/m:(Order).Save",
			Kind: domain.FactHasMethod, ToolSource: domain.ToolCodeGraph, Confidence: 1},
		{SourceID: "symbol:go:example.com/m/svc:Svc", TargetID: "symbol:go:example.com/m/svc:(Svc).Run",
			Kind: domain.FactHasMethod, ToolSource: domain.ToolCodeGraph, Confidence: 1},
		{SourceID: "symbol:go:example.com/m/svc:Svc", TargetID: "symbol:go:example.com/m/svc:(Svc).Load",
			Kind: domain.FactHasMethod, ToolSource: domain.ToolCodeGraph, Confidence: 1},
		// 实体对调用：Order.Save → Svc.Run（两次调用 → count 2）
		{SourceID: "symbol:go:example.com/m:(Order).Save", TargetID: "symbol:go:example.com/m/svc:(Svc).Run",
			Kind: domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.9},
	}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SaveBatchStats(nil, []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:(Order).Save", TargetID: "symbol:go:example.com/m/svc:(Svc).Run",
			Kind: domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.9},
	}, nil); err != nil {
		t.Fatal(err)
	}
	f := a.collectDomainFacts(DomainFactsRequest{})
	if len(f.TopPairs) == 0 {
		t.Fatalf("TopPairs 应非空（实体调用对热度）: %+v", f.TopPairs)
	}
	// 排序：Count 降序；确定性（同名对顺序稳定）
	for i := 1; i < len(f.TopPairs); i++ {
		if f.TopPairs[i].Count > f.TopPairs[i-1].Count {
			t.Errorf("TopPairs 应按 Count 降序: %+v", f.TopPairs)
		}
	}
	p := f.TopPairs[0]
	if p.From != "Order" || p.To != "Svc" || p.Count != 2 {
		t.Errorf("TopPairs[0] = %+v; want Order→Svc count=2（同义边合并累加）", p)
	}
}
