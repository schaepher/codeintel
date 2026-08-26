package action

// R34 domains 事实包增强字段测试（批次 C 随迁自 cli/wiki_r66/r70/r71
// 测试）：实体调用热度排序、包规模统计（ents）、struct service 标记、
// grpc 方法名截断（token 优化）。

import (
	"fmt"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// TestDomainFactsEntsHotFirst：R66——facts 实体按调用热度（Out+In）
// 降序截断——AI 看到核心实体。
func TestDomainFactsEntsHotFirst(t *testing.T) {
	a, dir := openRepoTest(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	// 3 实体热度不对称：hot 出 3 入 2（热=5）、low 出 1 入 3（4）、
	// w 出 2 入 1（3）——hot 应排第一（此前按字母序 low 在前）
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m/aa:low", Kind: domain.KindStruct, Name: "low", FilePath: "aa/low.go"},
		{ID: "symbol:go:example.com/m/aa:(low).m", Kind: domain.KindMethod, Name: "(low).m", FilePath: "aa/low.go"},
		{ID: "symbol:go:example.com/m/zz:hot", Kind: domain.KindStruct, Name: "hot", FilePath: "zz/hot.go"},
		{ID: "symbol:go:example.com/m/zz:(hot).m1", Kind: domain.KindMethod, Name: "(hot).m1", FilePath: "zz/hot.go"},
		{ID: "symbol:go:example.com/m/zz:(hot).m2", Kind: domain.KindMethod, Name: "(hot).m2", FilePath: "zz/hot.go"},
		{ID: "symbol:go:example.com/m/zz:(hot).m3", Kind: domain.KindMethod, Name: "(hot).m3", FilePath: "zz/hot.go"},
		{ID: "symbol:go:example.com/m/ww:w", Kind: domain.KindStruct, Name: "w", FilePath: "ww/w.go"},
		{ID: "symbol:go:example.com/m/ww:(w).m1", Kind: domain.KindMethod, Name: "(w).m1", FilePath: "ww/w.go"},
		{ID: "symbol:go:example.com/m/ww:(w).m2", Kind: domain.KindMethod, Name: "(w).m2", FilePath: "ww/w.go"},
	}, []*domain.Fact{
		{SourceID: "symbol:go:example.com/m/aa:low", TargetID: "symbol:go:example.com/m/aa:(low).m", Kind: domain.FactHasMethod, Confidence: 1.0},
		{SourceID: "symbol:go:example.com/m/zz:hot", TargetID: "symbol:go:example.com/m/zz:(hot).m1", Kind: domain.FactHasMethod, Confidence: 1.0},
		{SourceID: "symbol:go:example.com/m/zz:hot", TargetID: "symbol:go:example.com/m/zz:(hot).m2", Kind: domain.FactHasMethod, Confidence: 1.0},
		{SourceID: "symbol:go:example.com/m/zz:hot", TargetID: "symbol:go:example.com/m/zz:(hot).m3", Kind: domain.FactHasMethod, Confidence: 1.0},
		{SourceID: "symbol:go:example.com/m/ww:w", TargetID: "symbol:go:example.com/m/ww:(w).m1", Kind: domain.FactHasMethod, Confidence: 1.0},
		{SourceID: "symbol:go:example.com/m/ww:w", TargetID: "symbol:go:example.com/m/ww:(w).m2", Kind: domain.FactHasMethod, Confidence: 1.0},
		// hot 出 3（→low）；w 出 2（→hot）；low 出 1（→w）
		{SourceID: "symbol:go:example.com/m/zz:(hot).m1", TargetID: "symbol:go:example.com/m/aa:(low).m", Kind: domain.FactCalls, Confidence: 0.9},
		{SourceID: "symbol:go:example.com/m/zz:(hot).m2", TargetID: "symbol:go:example.com/m/aa:(low).m", Kind: domain.FactCalls, Confidence: 0.9},
		{SourceID: "symbol:go:example.com/m/zz:(hot).m3", TargetID: "symbol:go:example.com/m/aa:(low).m", Kind: domain.FactCalls, Confidence: 0.9},
		{SourceID: "symbol:go:example.com/m/ww:(w).m1", TargetID: "symbol:go:example.com/m/zz:(hot).m1", Kind: domain.FactCalls, Confidence: 0.9},
		{SourceID: "symbol:go:example.com/m/ww:(w).m2", TargetID: "symbol:go:example.com/m/zz:(hot).m1", Kind: domain.FactCalls, Confidence: 0.9},
		{SourceID: "symbol:go:example.com/m/aa:(low).m", TargetID: "symbol:go:example.com/m/ww:(w).m1", Kind: domain.FactCalls, Confidence: 0.9},
	}, nil); err != nil {
		t.Fatal(err)
	}
	f := a.collectDomainFacts(DomainFactsRequest{RepoAbs: dir})
	if len(f.Ents) < 2 {
		t.Fatalf("实体数 = %d; want ≥2", len(f.Ents))
	}
	if f.Ents[0].Name != "hot" {
		t.Errorf("热度排序后首个实体 = %s; want hot（Out+In 降序）", f.Ents[0].Name)
	}
}

// TestDomainFactsPkgEnts：R70——包规模统计（包内实体数）。
func TestDomainFactsPkgEnts(t *testing.T) {
	a, dir := openRepoTest(t)
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
	f := a.collectDomainFacts(DomainFactsRequest{RepoAbs: dir})
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
	a, dir := openRepoTest(t)
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
	f := a.collectDomainFacts(DomainFactsRequest{RepoAbs: dir})
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

// TestDomainFactsGrpcMethodsTrim：R71——grpc 服务方法名截断（前 20 +
// 总数——MemberService 100+ 方法全量是 token 大头；AI 多域归属判断
// 不需要全部方法名）。
func TestDomainFactsGrpcMethodsTrim(t *testing.T) {
	a, dir := openRepoTest(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	methods := make([]string, 0, 25)
	for i := 0; i < 25; i++ {
		methods = append(methods, fmt.Sprintf("Method%d", i))
	}
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m/grpc:svc.BigService", Kind: domain.KindGrpcService,
			Name: "svc.BigService", FilePath: "grpc/big_grpc.pb.go",
			Properties: map[string]any{"service_name": "BigService", "methods": strings.Join(methods, ",")}},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	f := a.collectDomainFacts(DomainFactsRequest{RepoAbs: dir})
	if len(f.Svcs) != 1 {
		t.Fatalf("服务数 = %d; want 1", len(f.Svcs))
	}
	s := f.Svcs[0]
	// 20 个方法名 + 1 个计数标注（"…共 25 个"）= 21 元素
	if len(s.Methods) > 21 {
		t.Errorf("方法名截断后 = %d; want ≤21（20 方法 + 计数标注）", len(s.Methods))
	}
	if !strings.HasSuffix(s.Methods[len(s.Methods)-1], "…共 25 个") {
		t.Errorf("末尾应标注总数: %q", s.Methods[len(s.Methods)-1])
	}
	if s.Methods[0] != "Method0" || s.Methods[19] != "Method19" {
		t.Errorf("前 20 个应为方法名: %q … %q", s.Methods[0], s.Methods[19])
	}
}
