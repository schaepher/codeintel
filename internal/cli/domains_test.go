package cli

// R34 domains 测试：AI 返回解析 + 校验（编造归属剔除）、事实包导出。
// 测试先行。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// TestParseDomainsValidate：AI 返回含编造包/表 → 剔除 + 警告；有效
// 归属保留。
func TestParseDomainsValidate(t *testing.T) {
	f := &domainFacts{
		Pkgs:   []pkgFacts{{Path: "item", Doc: "商品"}, {Path: "order", Doc: "订单"}, {Path: "member", Doc: "会员"}},
		Tables: []tableFacts{{Name: "item_info", Cols: 5}, {Name: "order_tab", Cols: 8}},
	}
	resp := `domains:
  - name: 商品域
    description: 商品管理
    packages: [item, ghost_pkg]
    tables: [item_info, ghost_table]
  - name: 订单域
    packages: [order]
    tables: [order_tab]
  - name: 空域
    packages: [nope]
`
	doms, warns := parseDomains(resp, f)
	if len(doms) != 2 {
		t.Fatalf("域数 = %d; want 2（空域剔除）:\n%+v", len(doms), doms)
	}
	if doms[0].Name != "商品域" || len(doms[0].Packages) != 1 || doms[0].Packages[0] != "item" {
		t.Errorf("商品域 = %+v; want packages=[item]（ghost_pkg 剔除）", doms[0])
	}
	if len(doms[0].Tables) != 1 || doms[0].Tables[0] != "item_info" {
		t.Errorf("商品域 tables = %+v; want [item_info]（ghost_table 剔除）", doms[0].Tables)
	}
	if len(warns) < 3 {
		t.Errorf("警告数 = %d; want ≥3（ghost_pkg/ghost_table/空域）:\n%v", len(warns), warns)
	}
}

// TestParseDomainsFence：```yaml 围栏剥离。
func TestParseDomainsFence(t *testing.T) {
	f := &domainFacts{Pkgs: []pkgFacts{{Path: "item"}}, Tables: []tableFacts{{Name: "item_info"}}}
	resp := "```yaml\ndomains:\n  - name: 商品域\n    packages: [item]\n    tables: [item_info]\n```"
	doms, _ := parseDomains(resp, f)
	if len(doms) != 1 || doms[0].Name != "商品域" {
		t.Errorf("围栏解析失败: %+v", doms)
	}
}

// TestExportDomainFacts：事实包导出文件——JSON 格式（用户要求），
// 结构字段齐全。
func TestExportDomainFacts(t *testing.T) {
	dir := seedRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	acts := action.New(sqlite.NewRepo(db))
	path := filepath.Join(t.TempDir(), "facts.json")
	if err := exportDomainFacts(dir, acts, wikiConfig{}, sqlite.NewRepo(db), path); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	var f domainFacts
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatalf("导出应为合法 JSON: %v", err)
	}
	// 结构字段（JSON 契约）
	for _, want := range []string{`"packages"`, `"tables"`, `"entities"`, `"services"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("JSON 缺 %q:\n%s", want, b)
		}
	}
}

// TestDomainFactsJSONCompact：R61——AI 读取的事实包不 format（compact
// JSON——避免文件过大消耗 token）；--export-facts 人工检查用缩进版。
func TestDomainFactsJSONCompact(t *testing.T) {
	f := &domainFacts{Pkgs: []pkgFacts{{Path: "example.com/m", Doc: "主包"}},
		Tables: []tableFacts{{Name: "orders"}},
	}
	b, err := domainFactsJSON(f)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "\n  ") {
		t.Errorf("AI 读取的事实包不应缩进（compact）:\n%s", b)
	}
	bi, err := domainFactsJSONIndent(f)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(bi), "\n  ") {
		t.Error("缩进版（--export-facts）应含换行缩进")
	}
	if len(b) >= len(bi) {
		t.Errorf("compact 应小于缩进版（%d >= %d）", len(b), len(bi))
	}
}

// TestDomainFactsEntityOutIn：R64——实体带出度/入度（调用边数——
// AI 划分领域时参考调用热度：高内聚实体同域、领域间调用少）。
func TestDomainFactsEntityOutIn(t *testing.T) {
	dir := seedRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	// 实体图：A→B（2 个方法 target——不同 calls 边不合并，Count=2）、
	// A→C（1）、C→B（1）。实体 = 有方法（has_method 边）的类型。
	acts := action.New(r)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:a", Kind: domain.KindStruct, Name: "a", FilePath: "a.go"},
		{ID: "symbol:go:example.com/m:(a).m1", Kind: domain.KindMethod, Name: "(a).m1", FilePath: "a.go"},
		{ID: "symbol:go:example.com/m:b", Kind: domain.KindStruct, Name: "b", FilePath: "b.go"},
		{ID: "symbol:go:example.com/m:(b).m1", Kind: domain.KindMethod, Name: "(b).m1", FilePath: "b.go"},
		{ID: "symbol:go:example.com/m:(b).m2", Kind: domain.KindMethod, Name: "(b).m2", FilePath: "b.go"},
		{ID: "symbol:go:example.com/m:c", Kind: domain.KindStruct, Name: "c", FilePath: "c.go"},
		{ID: "symbol:go:example.com/m:(c).m1", Kind: domain.KindMethod, Name: "(c).m1", FilePath: "c.go"},
	}, []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:a", TargetID: "symbol:go:example.com/m:(a).m1", Kind: domain.FactHasMethod, Confidence: 1.0},
		{SourceID: "symbol:go:example.com/m:b", TargetID: "symbol:go:example.com/m:(b).m1", Kind: domain.FactHasMethod, Confidence: 1.0},
		{SourceID: "symbol:go:example.com/m:b", TargetID: "symbol:go:example.com/m:(b).m2", Kind: domain.FactHasMethod, Confidence: 1.0},
		{SourceID: "symbol:go:example.com/m:c", TargetID: "symbol:go:example.com/m:(c).m1", Kind: domain.FactHasMethod, Confidence: 1.0},
		// 方法级 calls：(a).m1→(b).m1、(a).m1→(b).m2（A→B Count=2）、
		// (a).m1→(c).m1（A→C Count=1）、(c).m1→(b).m1（C→B Count=1）
		{SourceID: "symbol:go:example.com/m:(a).m1", TargetID: "symbol:go:example.com/m:(b).m1", Kind: domain.FactCalls, Confidence: 0.9},
		{SourceID: "symbol:go:example.com/m:(a).m1", TargetID: "symbol:go:example.com/m:(b).m2", Kind: domain.FactCalls, Confidence: 0.9},
		{SourceID: "symbol:go:example.com/m:(a).m1", TargetID: "symbol:go:example.com/m:(c).m1", Kind: domain.FactCalls, Confidence: 0.9},
		{SourceID: "symbol:go:example.com/m:(c).m1", TargetID: "symbol:go:example.com/m:(b).m1", Kind: domain.FactCalls, Confidence: 0.9},
	}, nil); err != nil {
		t.Fatal(err)
	}
	f := collectDomainFacts(acts, dir, wikiConfig{}, r)
	byName := map[string]entityFacts{}
	for _, e := range f.Ents {
		byName[e.Name] = e
	}
	a, okA := byName["a"]
	b, okB := byName["b"]
	c, okC := byName["c"]
	if !okA || !okB || !okC {
		t.Fatalf("实体缺失 a/b/c: %+v", byName)
	}
	// R66：out/in = 调用次数聚合（Count 累加）——A→B×2 + A→C×1 = 3；
	// B 被调 A→B×2 + C→B×1 = 3（2 方法实体保留——门槛滤 1 方法 0 出边）；
	// C 调出 1（C→B）、被调 1（A→C）
	if a.Out != 3 || a.In != 0 {
		t.Errorf("A out/in = %d/%d; want 3/0（Count 累加）", a.Out, a.In)
	}
	if b.Out != 0 || b.In != 3 {
		t.Errorf("B out/in = %d/%d; want 0/3", b.Out, b.In)
	}
	if c.Out != 1 || c.In != 1 {
		t.Errorf("C out/in = %d/%d; want 1/1", c.Out, c.In)
	}
}

// TestDomainFactsEntityPkg：R65——实体带包路径（AI 归属实体→包→子域）。
func TestDomainFactsEntityPkg(t *testing.T) {
	dir := seedRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m/order:svc", Kind: domain.KindStruct, Name: "svc", FilePath: "order/svc.go"},
		{ID: "symbol:go:example.com/m/order:(svc).Run", Kind: domain.KindMethod, Name: "(svc).Run", FilePath: "order/svc.go"},
	}, []*domain.Fact{
		{SourceID: "symbol:go:example.com/m/order:svc", TargetID: "symbol:go:example.com/m/order:(svc).Run", Kind: domain.FactHasMethod, Confidence: 1.0},
	}, nil); err != nil {
		t.Fatal(err)
	}
	f := collectDomainFacts(action.New(r), dir, wikiConfig{}, r)
	for _, e := range f.Ents {
		if e.Name == "svc" && e.Pkg != "example.com/m/order" {
			t.Errorf("实体 Pkg = %q; want example.com/m/order（完整路径——与 packages 对应）", e.Pkg)
		}
	}
	if len(f.Ents) > 0 && f.Ents[0].Pkg == "" {
		t.Error("实体应带 Pkg 字段")
	}
}

// TestDomainFactsPkgCalls：R65——包级调用矩阵（跨包聚合；同包调用与
// 子域划分无关不计）。
func TestDomainFactsPkgCalls(t *testing.T) {
	dir := seedRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	// 实体：x/y 同包 order、z 在 pay——x→y 同包不计；x→z 跨包计 3 次
	// （edges UNIQUE(source,target,kind) 同义边合并——Count>1 来自不同
	// 方法对聚合：z 用 3 个方法）
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m/order:x", Kind: domain.KindStruct, Name: "x", FilePath: "order/x.go"},
		{ID: "symbol:go:example.com/m/order:(x).m", Kind: domain.KindMethod, Name: "(x).m", FilePath: "order/x.go"},
		{ID: "symbol:go:example.com/m/order:y", Kind: domain.KindStruct, Name: "y", FilePath: "order/y.go"},
		{ID: "symbol:go:example.com/m/order:(y).m", Kind: domain.KindMethod, Name: "(y).m", FilePath: "order/y.go"},
		{ID: "symbol:go:example.com/m/pay:z", Kind: domain.KindStruct, Name: "z", FilePath: "pay/z.go"},
		{ID: "symbol:go:example.com/m/pay:(z).m1", Kind: domain.KindMethod, Name: "(z).m1", FilePath: "pay/z.go"},
		{ID: "symbol:go:example.com/m/pay:(z).m2", Kind: domain.KindMethod, Name: "(z).m2", FilePath: "pay/z.go"},
		{ID: "symbol:go:example.com/m/pay:(z).m3", Kind: domain.KindMethod, Name: "(z).m3", FilePath: "pay/z.go"},
	}, []*domain.Fact{
		{SourceID: "symbol:go:example.com/m/order:x", TargetID: "symbol:go:example.com/m/order:(x).m", Kind: domain.FactHasMethod, Confidence: 1.0},
		{SourceID: "symbol:go:example.com/m/order:y", TargetID: "symbol:go:example.com/m/order:(y).m", Kind: domain.FactHasMethod, Confidence: 1.0},
		{SourceID: "symbol:go:example.com/m/pay:z", TargetID: "symbol:go:example.com/m/pay:(z).m1", Kind: domain.FactHasMethod, Confidence: 1.0},
		{SourceID: "symbol:go:example.com/m/pay:z", TargetID: "symbol:go:example.com/m/pay:(z).m2", Kind: domain.FactHasMethod, Confidence: 1.0},
		{SourceID: "symbol:go:example.com/m/pay:z", TargetID: "symbol:go:example.com/m/pay:(z).m3", Kind: domain.FactHasMethod, Confidence: 1.0},
		// (x).m → (y).m（同包——不计）；(x).m → z 的 3 个方法（跨包——计 3）；
		// (z).m1 → (x).m（跨包——z 需出边过行为门槛，计 1）
		{SourceID: "symbol:go:example.com/m/order:(x).m", TargetID: "symbol:go:example.com/m/order:(y).m", Kind: domain.FactCalls, Confidence: 0.9},
		{SourceID: "symbol:go:example.com/m/order:(x).m", TargetID: "symbol:go:example.com/m/pay:(z).m1", Kind: domain.FactCalls, Confidence: 0.9},
		{SourceID: "symbol:go:example.com/m/order:(x).m", TargetID: "symbol:go:example.com/m/pay:(z).m2", Kind: domain.FactCalls, Confidence: 0.9},
		{SourceID: "symbol:go:example.com/m/order:(x).m", TargetID: "symbol:go:example.com/m/pay:(z).m3", Kind: domain.FactCalls, Confidence: 0.9},
		{SourceID: "symbol:go:example.com/m/pay:(z).m1", TargetID: "symbol:go:example.com/m/order:(x).m", Kind: domain.FactCalls, Confidence: 0.9},
	}, nil); err != nil {
		t.Fatal(err)
	}
	f := collectDomainFacts(action.New(r), dir, wikiConfig{}, r)
	if len(f.PkgCalls) != 2 {
		t.Fatalf("pkg_calls = %+v; want 2 条跨包边", f.PkgCalls)
	}
	byPair := map[string]pkgCallFacts{}
	for _, pc := range f.PkgCalls {
		byPair[pc.From+"|"+pc.To] = pc
	}
	if pc := byPair["example.com/m/order|example.com/m/pay"]; pc.Count != 3 {
		t.Errorf("order→pay count = %d; want 3", pc.Count)
	}
	if pc := byPair["example.com/m/pay|example.com/m/order"]; pc.Count != 1 {
		t.Errorf("pay→order count = %d; want 1", pc.Count)
	}
}

// TestDomainFactsGrpcMethods：R54——grpc 服务带方法名列表（一个服务
// 可能含多域方法、分开部署——方法级归属信息）。
func TestDomainFactsGrpcMethods(t *testing.T) {
	dir := seedGrpcRoutesRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	acts := action.New(sqlite.NewRepo(db))
	f := collectDomainFacts(acts, dir, wikiConfig{}, sqlite.NewRepo(db))
	found := false
	for _, s := range f.Svcs {
		if s.Type == "grpc" && s.Name == "QueryService" {
			found = true
			if len(s.Methods) == 0 {
				t.Fatal("grpc 服务应带方法名列表（R54）")
			}
			joined := strings.Join(s.Methods, ",")
			if !strings.Contains(joined, "Query") || !strings.Contains(joined, "PagingShops") {
				t.Errorf("QueryService.Methods = %v; want 含 Query,PagingShops", s.Methods)
			}
		}
	}
	if !found {
		t.Error("事实包应含 grpc QueryService 服务")
	}
}
