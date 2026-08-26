package cli

// R70 facts 包规模统计 + 实体 service 区分测试。

import (
	"testing"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// TestDomainFactsPkgEnts：R70——包规模统计（包内实体数）。
func TestDomainFactsPkgEnts(t *testing.T) {
	dir := seedRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	// order 包 2 实体 + pay 包 1 实体（需包节点——packages 来自 KindPackage）
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m/order:order", Kind: domain.KindPackage, Name: "order", FilePath: "order/"},
		{ID: "symbol:go:example.com/m/pay:pay", Kind: domain.KindPackage, Name: "pay", FilePath: "pay/"},
		{ID: "symbol:go:example.com/m/order:svc1", Kind: domain.KindStruct, Name: "svc1", FilePath: "order/svc1.go"},
		{ID: "symbol:go:example.com/m/order:(svc1).A", Kind: domain.KindMethod, Name: "(svc1).A", FilePath: "order/svc1.go"},
		{ID: "symbol:go:example.com/m/order:(svc1).B", Kind: domain.KindMethod, Name: "(svc1).B", FilePath: "order/svc1.go"},
		{ID: "symbol:go:example.com/m/order:svc2", Kind: domain.KindStruct, Name: "svc2", FilePath: "order/svc2.go"},
		{ID: "symbol:go:example.com/m/order:(svc2).C", Kind: domain.KindMethod, Name: "(svc2).C", FilePath: "order/svc2.go"},
		{ID: "symbol:go:example.com/m/order:(svc2).D", Kind: domain.KindMethod, Name: "(svc2).D", FilePath: "order/svc2.go"},
		{ID: "symbol:go:example.com/m/pay:p1", Kind: domain.KindStruct, Name: "p1", FilePath: "pay/p1.go"},
		{ID: "symbol:go:example.com/m/pay:(p1).X", Kind: domain.KindMethod, Name: "(p1).X", FilePath: "pay/p1.go"},
		{ID: "symbol:go:example.com/m/pay:(p1).Y", Kind: domain.KindMethod, Name: "(p1).Y", FilePath: "pay/p1.go"},
	}, []*domain.Fact{
		{SourceID: "symbol:go:example.com/m/order:svc1", TargetID: "symbol:go:example.com/m/order:(svc1).A", Kind: domain.FactHasMethod, Confidence: 1.0},
		{SourceID: "symbol:go:example.com/m/order:svc1", TargetID: "symbol:go:example.com/m/order:(svc1).B", Kind: domain.FactHasMethod, Confidence: 1.0},
		{SourceID: "symbol:go:example.com/m/order:svc2", TargetID: "symbol:go:example.com/m/order:(svc2).C", Kind: domain.FactHasMethod, Confidence: 1.0},
		{SourceID: "symbol:go:example.com/m/order:svc2", TargetID: "symbol:go:example.com/m/order:(svc2).D", Kind: domain.FactHasMethod, Confidence: 1.0},
		{SourceID: "symbol:go:example.com/m/pay:p1", TargetID: "symbol:go:example.com/m/pay:(p1).X", Kind: domain.FactHasMethod, Confidence: 1.0},
		{SourceID: "symbol:go:example.com/m/pay:p1", TargetID: "symbol:go:example.com/m/pay:(p1).Y", Kind: domain.FactHasMethod, Confidence: 1.0},
	}, nil); err != nil {
		t.Fatal(err)
	}
	f := collectDomainFacts(action.New(r), dir, wikiConfig{})
	byPkg := map[string]int{}
	for _, p := range f.Pkgs {
		byPkg[p.Path] = p.Ents
	}
	if byPkg["example.com/m/order"] != 2 {
		t.Errorf("order 包 ents = %d; want 2", byPkg["example.com/m/order"])
	}
	if byPkg["example.com/m/pay"] != 1 {
		t.Errorf("pay 包 ents = %d; want 1", byPkg["example.com/m/pay"])
	}
}

// TestDomainFactsEntityService：R70——entityFacts 带 Service 标记
// （行为载体 vs 数据载体——AI 划分时区分）。
func TestDomainFactsEntityService(t *testing.T) {
	dir := seedRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	// Svc（无写）→ service；Data（有写）→ 非 service
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m/s:Svc", Kind: domain.KindStruct, Name: "Svc", FilePath: "s/svc.go"},
		{ID: "symbol:go:example.com/m/s:(Svc).Run", Kind: domain.KindMethod, Name: "(Svc).Run", FilePath: "s/svc.go"},
		{ID: "symbol:go:example.com/m/s:(Svc).Stop", Kind: domain.KindMethod, Name: "(Svc).Stop", FilePath: "s/svc.go"},
		{ID: "symbol:go:example.com/m/d:Data", Kind: domain.KindStruct, Name: "Data", FilePath: "d/data.go"},
		{ID: "symbol:go:example.com/m/d:(Data).Load", Kind: domain.KindMethod, Name: "(Data).Load", FilePath: "d/data.go"},
		{ID: "symbol:go:example.com/m/d:(Data).Save", Kind: domain.KindMethod, Name: "(Data).Save", FilePath: "d/data.go"},
	}, []*domain.Fact{
		{SourceID: "symbol:go:example.com/m/s:Svc", TargetID: "symbol:go:example.com/m/s:(Svc).Run", Kind: domain.FactHasMethod, Confidence: 1.0},
		{SourceID: "symbol:go:example.com/m/s:Svc", TargetID: "symbol:go:example.com/m/s:(Svc).Stop", Kind: domain.FactHasMethod, Confidence: 1.0},
		{SourceID: "symbol:go:example.com/m/d:Data", TargetID: "symbol:go:example.com/m/d:(Data).Load", Kind: domain.FactHasMethod, Confidence: 1.0},
		{SourceID: "symbol:go:example.com/m/d:Data", TargetID: "symbol:go:example.com/m/d:(Data).Save", Kind: domain.FactHasMethod, Confidence: 1.0},
	}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SaveBatchStats(nil, nil, []*domain.FunctionFieldSummary{
		{FunctionID: "symbol:go:example.com/m/d:(Data).Load", AccessKind: domain.SummaryDirectWrite,
			FieldPath: "example.com/m/d.Data.v", InstancePath: "d.v", LineStart: 5},
	}); err != nil {
		t.Fatal(err)
	}
	f := collectDomainFacts(action.New(r), dir, wikiConfig{})
	svc := map[string]bool{}
	for _, e := range f.Ents {
		svc[e.Name] = e.Service
	}
	if !svc["Svc"] {
		t.Error("Svc（无字段写）应标记 service")
	}
	if svc["Data"] {
		t.Error("Data（字段被写）不应标记 service")
	}
}
