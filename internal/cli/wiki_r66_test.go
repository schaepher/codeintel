package cli

// R66 实体定义收敛测试：接口 0 方法门槛、门面排除 .pb.go 生成代码、
// facts 实体按调用热度截断。

import (
	"testing"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// TestEntityIfaceNoMethods：R66——0 方法接口（标记接口）滤除；有方法
// 接口（receiver 匹配 method 节点）保留。
func TestEntityIfaceNoMethods(t *testing.T) {
	dir := seedRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:Marker", Kind: domain.KindInterface, Name: "Marker", FilePath: "m.go"},
		{ID: "symbol:go:example.com/m:Service", Kind: domain.KindInterface, Name: "Service", FilePath: "m.go"},
		{ID: "symbol:go:example.com/m:(Service).Run", Kind: domain.KindMethod, Name: "(Service).Run", FilePath: "m.go"},
		{ID: "symbol:go:example.com/m/impl:s", Kind: domain.KindStruct, Name: "s", FilePath: "impl/s.go"},
		{ID: "symbol:go:example.com/m/impl:(s).Run", Kind: domain.KindMethod, Name: "(s).Run", FilePath: "impl/s.go"},
	}, []*domain.Fact{
		{SourceID: "symbol:go:example.com/m/impl:s", TargetID: "symbol:go:example.com/m/impl:(s).Run", Kind: domain.FactHasMethod, Confidence: 1.0},
		// 接口方法调用（接口方法参与调用——通过 impl 或直接）
		{SourceID: "symbol:go:example.com/m/impl:(s).Run", TargetID: "symbol:go:example.com/m:(Service).Run", Kind: domain.FactCalls, Confidence: 0.9},
	}, nil); err != nil {
		t.Fatal(err)
	}
	g, err := action.New(r).Entities()
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, n := range g.Nodes {
		names[n.Name] = true
	}
	if names["Marker"] {
		t.Error("0 方法接口（标记接口）应被滤除（R66 实体收敛）")
	}
	if !names["Service"] {
		t.Error("有方法接口应保留（receiver 匹配 method 节点）")
	}
	if !names["s"] {
		t.Error("实现类型 s 应保留")
	}
}

// TestEntityFaceExcludesPB：R66——门面游离函数排除 .pb.go 生成代码
// （proto 包 640 个生成函数不建门面）。
func TestEntityFaceExcludesPB(t *testing.T) {
	dir := seedRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	// 包 gen：6 个游离函数全在 .pb.go → 不应建门面
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m/gen:pkg", Kind: domain.KindPackage, Name: "pkg", FilePath: "gen/pb.go"},
	}
	facts := []*domain.Fact{}
	for i := 0; i < 6; i++ {
		nodes = append(nodes, &domain.CodeEntity{
			ID: domain.CanonicalID("symbol:go:example.com/m/gen:f" + itoa(i)), Kind: domain.KindFunction,
			Name: "f" + itoa(i), FilePath: "gen/query.pb.go"})
		_ = facts
	}
	if _, err := r.SaveBatchStats(nodes, nil, nil); err != nil {
		t.Fatal(err)
	}
	g, err := action.New(r).Entities()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range g.Nodes {
		if n.Kind == domain.EntityKindPkgFace {
			t.Errorf("纯 .pb.go 游离函数的包不应建门面（生成代码噪音）: %s", n.Name)
		}
	}
}

// TestDomainFactsEntsHotFirst：R66——facts 实体按调用热度（Out+In）
// 降序截断——AI 看到核心实体。
func TestDomainFactsEntsHotFirst(t *testing.T) {
	dir := seedRepo(t)
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
	f := collectDomainFacts(action.New(r), dir, wikiConfig{}, r)
	if len(f.Ents) < 2 {
		t.Fatalf("实体数 = %d; want ≥2", len(f.Ents))
	}
	if f.Ents[0].Name != "hot" {
		t.Errorf("热度排序后首个实体 = %s; want hot（Out+In 降序）", f.Ents[0].Name)
	}
}

