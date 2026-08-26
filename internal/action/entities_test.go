package action

// R9 实体协作图测试：Entities() 聚合（类型实体 + 包门面实体 + 实体间
// 边计数 + 实体内互调）+ 4 类设计诊断（高耦合/循环/上帝对象/游离函数
// 占比）。fixture 手写节点（参照 seedRepo 模式），不依赖索引管道。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// entitiesFixture 建小仓库：包 m 有 Svc（2 方法，互调 + 调 Repo）、
// Repo（2 方法）、DTO（无方法——非实体）+ 6 个游离函数（≥5 → m 门面）。
func entitiesFixture(t *testing.T) *Actions {
	t.Helper()
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)

	typeID := func(n string) string { return "symbol:go:example.com/m:" + n }
	methodID := func(recv, name string) string {
		return "symbol:go:example.com/m:(" + recv + ")." + name
	}
	funcID := func(n string) string { return "symbol:go:example.com/m:" + n }

	nodes := []*domain.CodeEntity{
		{ID: domain.CanonicalID(typeID("Svc")), Kind: domain.KindStruct, Name: "Svc"},
		{ID: domain.CanonicalID(typeID("Repo")), Kind: domain.KindStruct, Name: "Repo"},
		{ID: domain.CanonicalID(typeID("DTO")), Kind: domain.KindStruct, Name: "DTO"}, // 无方法 → 非实体
		{ID: domain.CanonicalID(methodID("Svc", "Run")), Kind: domain.KindMethod, Name: "(Svc).Run"},
		{ID: domain.CanonicalID(methodID("Svc", "Stop")), Kind: domain.KindMethod, Name: "(Svc).Stop"},
		{ID: domain.CanonicalID(methodID("Repo", "Find")), Kind: domain.KindMethod, Name: "(Repo).Find"},
		{ID: domain.CanonicalID(methodID("Repo", "Insert")), Kind: domain.KindMethod, Name: "(Repo).Insert"},
		// 游离函数（6 个 ≥ 5 → m 门面实体）
		{ID: domain.CanonicalID(funcID("main")), Kind: domain.KindFunction, Name: "main"},
		{ID: domain.CanonicalID(funcID("f1")), Kind: domain.KindFunction, Name: "f1"},
		{ID: domain.CanonicalID(funcID("f2")), Kind: domain.KindFunction, Name: "f2"},
		{ID: domain.CanonicalID(funcID("f3")), Kind: domain.KindFunction, Name: "f3"},
		{ID: domain.CanonicalID(funcID("f4")), Kind: domain.KindFunction, Name: "f4"},
		{ID: domain.CanonicalID(funcID("f5")), Kind: domain.KindFunction, Name: "f5"},
	}
	var facts []*domain.Fact
	add := func(src, dst string) {
		facts = append(facts, &domain.Fact{SourceID: domain.CanonicalID(src), TargetID: domain.CanonicalID(dst),
			Kind: domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.9})
	}
	// has_method
	for _, hm := range []struct{ t, recv, m string }{
		{"Svc", "Svc", "Run"}, {"Svc", "Svc", "Stop"},
		{"Repo", "Repo", "Find"}, {"Repo", "Repo", "Insert"},
	} {
		facts = append(facts, &domain.Fact{SourceID: domain.CanonicalID(typeID(hm.t)), TargetID: domain.CanonicalID(methodID(hm.recv, hm.m)),
			Kind: domain.FactHasMethod, ToolSource: domain.ToolCodeGraph, Confidence: 1})
	}
	// calls：main → Svc 方法 + f1；Svc.Run → Repo.Find；Svc.Run→Svc.Stop（内互调）
	add(funcID("main"), methodID("Svc", "Run"))
	add(funcID("main"), methodID("Svc", "Stop"))
	add(funcID("main"), funcID("f1"))
	add(methodID("Svc", "Run"), methodID("Repo", "Find"))
	add(methodID("Svc", "Run"), methodID("Svc", "Stop"))
	add(funcID("f1"), methodID("Svc", "Run"))
	add(funcID("f2"), funcID("f3"))
	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatal(err)
	}
	return New(r)
}

// TestEntitiesBasic：类型实体 + 门面实体 + 边聚合 + 内互调。
func TestEntitiesBasic(t *testing.T) {
	a := entitiesFixture(t)
	g, err := a.Entities()
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*domain.EntityNode{}
	for _, n := range g.Nodes {
		byName[n.Name] = n
	}
	// 3 个实体：Svc / Repo / m（门面）；DTO 无方法 → 过滤
	if len(g.Nodes) != 3 {
		t.Fatalf("实体数 = %d, want 3（%v）", len(g.Nodes), names(g.Nodes))
	}
	if _, ok := byName["DTO"]; ok {
		t.Error("DTO 无方法，不应是实体")
	}
	svc, repo, face := byName["Svc"], byName["Repo"], byName["m"]
	if svc.Kind != "struct" || svc.MethodCount != 2 {
		t.Errorf("Svc = %s/%d 方法, want struct/2", svc.Kind, svc.MethodCount)
	}
	if repo.MethodCount != 2 {
		t.Errorf("Repo 方法数 = %d, want 2", repo.MethodCount)
	}
	if face.Kind != "pkg-face" || face.FreeFuncs != 6 {
		t.Errorf("m 门面 = %s/%d 游离函数, want pkg-face/6", face.Kind, face.FreeFuncs)
	}
	// 实体内互调：Svc.Run → Svc.Stop
	if svc.InnerCalls != 1 {
		t.Errorf("Svc 内互调 = %d, want 1", svc.InnerCalls)
	}
	// 实体间边：Svc→Repo 1 次
	found := false
	for _, e := range g.Edges {
		if e.From == svc.ID && e.To == repo.ID && e.Count == 1 {
			found = true
		}
	}
	if !found {
		t.Errorf("缺少 Svc→Repo 边 count=1: %+v", g.Edges)
	}
	// 门面→Svc：main 调 Svc 方法 2 次 + f1 调 1 次 = 3
	for _, e := range g.Edges {
		if e.From == face.ID && e.To == svc.ID && e.Count == 3 {
			return
		}
	}
	t.Errorf("缺少 m→Svc 边 count=3: %+v", g.Edges)
}

// TestEntitiesNoFaceSmallPkg：游离函数 < 5 的包不建门面，其调用边丢弃。
func TestEntitiesNoFaceSmallPkg(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	goID := "symbol:go:example.com/small:(T).Go"
	go2ID := "symbol:go:example.com/small:(T).Go2"
	soloID := "symbol:go:example.com/small:solo"
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/small:T", Kind: domain.KindStruct, Name: "T"},
		{ID: domain.CanonicalID(goID), Kind: domain.KindMethod, Name: "(T).Go"},
		{ID: domain.CanonicalID(go2ID), Kind: domain.KindMethod, Name: "(T).Go2"},
		{ID: domain.CanonicalID(soloID), Kind: domain.KindFunction, Name: "solo"},
	}, []*domain.Fact{
		{SourceID: "symbol:go:example.com/small:T", TargetID: domain.CanonicalID(goID), Kind: domain.FactHasMethod, ToolSource: domain.ToolCodeGraph, Confidence: 1},
		{SourceID: "symbol:go:example.com/small:T", TargetID: domain.CanonicalID(go2ID), Kind: domain.FactHasMethod, ToolSource: domain.ToolCodeGraph, Confidence: 1},
		{SourceID: domain.CanonicalID(soloID), TargetID: domain.CanonicalID(goID), Kind: domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.9},
	}, nil); err != nil {
		t.Fatal(err)
	}
	g, err := New(r).Entities()
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) != 1 || g.Nodes[0].Name != "T" {
		t.Fatalf("实体 = %v, want 仅 T（无门面）", names(g.Nodes))
	}
	if len(g.Edges) != 0 {
		t.Errorf("游离函数 <5 时 solo→T 边应丢弃: %+v", g.Edges)
	}
}

func names(ns []*domain.EntityNode) []string {
	var out []string
	for _, n := range ns {
		out = append(out, n.Name)
	}
	return out
}

// entitiesDiagFixture 造诊断触发图：A→B 高耦合（20 边）、跨包循环
// (m1)A→(m2)B→(m1)A、上帝对象 G（出边 15）、facepkg 游离函数占比超标。
func entitiesDiagFixture(t *testing.T) *Actions {
	t.Helper()
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)

	methodID := func(pkg, recv, name string) string {
		return "symbol:go:example.com/" + pkg + ":(" + recv + ")." + name
	}
	typeID := func(pkg, n string) string { return "symbol:go:example.com/" + pkg + ":" + n }
	funcID := func(pkg, n string) string { return "symbol:go:example.com/" + pkg + ":" + n }

	var nodes []*domain.CodeEntity
	var facts []*domain.Fact
	add := func(src, dst string) {
		facts = append(facts, &domain.Fact{SourceID: domain.CanonicalID(src), TargetID: domain.CanonicalID(dst),
			Kind: domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.9})
	}
	hasM := func(t, m string) {
		facts = append(facts, &domain.Fact{SourceID: domain.CanonicalID(t), TargetID: domain.CanonicalID(m),
			Kind: domain.FactHasMethod, ToolSource: domain.ToolCodeGraph, Confidence: 1})
	}
	mkType := func(id string) {
		nodes = append(nodes, &domain.CodeEntity{ID: domain.CanonicalID(id), Kind: domain.KindStruct, Name: id[strings.LastIndex(id, ":")+1:]})
	}
	mkMethod := func(id string) {
		nodes = append(nodes, &domain.CodeEntity{ID: domain.CanonicalID(id), Kind: domain.KindMethod, Name: id[strings.LastIndex(id, ":")+1:]})
	}
	mkFunc := func(id string) {
		nodes = append(nodes, &domain.CodeEntity{ID: domain.CanonicalID(id), Kind: domain.KindFunction, Name: id[strings.LastIndex(id, ":")+1:]})
	}

	// 高耦合对（跨包）：hc1.A 的 4 方法 × hc2.B 的 5 方法 = 20 条边
	mkType(typeID("hc1", "A"))
	mkType(typeID("hc2", "B"))
	for i := 0; i < 4; i++ {
		mID := methodID("hc1", "A", "M"+string(rune('0'+i)))
		mkMethod(mID)
		hasM(typeID("hc1", "A"), mID)
	}
	for i := 0; i < 5; i++ {
		mID := methodID("hc2", "B", "N"+string(rune('0'+i)))
		mkMethod(mID)
		hasM(typeID("hc2", "B"), mID)
	}
	for i := 0; i < 4; i++ {
		for j := 0; j < 5; j++ {
			add(methodID("hc1", "A", "M"+string(rune('0'+i))), methodID("hc2", "B", "N"+string(rune('0'+j))))
		}
	}

	// 跨包循环：(m1).LoopA → (m2).LoopB → (m1).LoopA
	mkType(typeID("m1", "LoopA"))
	mkType(typeID("m2", "LoopB"))
	mkMethod(methodID("m1", "LoopA", "Go"))
	mkMethod(methodID("m2", "LoopB", "Go"))
	hasM(typeID("m1", "LoopA"), methodID("m1", "LoopA", "Go"))
	hasM(typeID("m2", "LoopB"), methodID("m2", "LoopB", "Go"))
	add(methodID("m1", "LoopA", "Go"), methodID("m2", "LoopB", "Go"))
	add(methodID("m2", "LoopB", "Go"), methodID("m1", "LoopA", "Go"))

	// 上帝对象 G：1 方法调 20 个外部游离函数（出边 ≥ 20）
	mkType(typeID("god", "G"))
	mkMethod(methodID("god", "G", "Do"))
	hasM(typeID("god", "G"), methodID("god", "G", "Do"))
	for i := 0; i < 20; i++ {
		fID := funcID("lib", "ext"+string(rune('0'+i)))
		mkFunc(fID)
		add(methodID("god", "G", "Do"), fID)
	}

	// facepkg：10 游离函数 + 1 类型 1 方法 → 游离 > 方法
	mkType(typeID("facepkg", "Only"))
	mkMethod(methodID("facepkg", "Only", "Go"))
	hasM(typeID("facepkg", "Only"), methodID("facepkg", "Only", "Go"))
	for i := 0; i < 10; i++ {
		mkFunc(funcID("facepkg", "util"+string(rune('0'+i))))
	}

	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatal(err)
	}
	return New(r)
}

// TestEntitiesDiagnostics：4 类设计诊断各自触发。
func TestEntitiesDiagnostics(t *testing.T) {
	a := entitiesDiagFixture(t)
	g, err := a.Entities()
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, d := range g.Diags {
		kinds[d.Kind] = true
	}
	for _, want := range []string{"coupled", "cycle", "god-object", "face-heavy"} {
		if !kinds[want] {
			t.Errorf("缺少诊断 %s；实际 %v", want, g.Diags)
		}
	}
	// coupled 指向 hc1.A→hc2.B（跨包 + 包前缀消歧）
	found := false
	for _, d := range g.Diags {
		if d.Kind == "coupled" && d.Target == "hc1.A→hc2.B" {
			found = true
		}
	}
	if !found {
		t.Errorf("coupled 应指向 hc1.A→hc2.B（20 次）: %+v", g.Diags)
	}
}
