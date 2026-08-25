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
