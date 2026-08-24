package cli

// R34 domains 测试：AI 返回解析 + 校验（编造归属剔除）、事实包导出。
// 测试先行。

import (
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
		Tables: []string{"item_info（5 列）", "order_tab（8 列）"},
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
	f := &domainFacts{Pkgs: []pkgFacts{{Path: "item"}}, Tables: []string{"item_info"}}
	resp := "```yaml\ndomains:\n  - name: 商品域\n    packages: [item]\n    tables: [item_info]\n```"
	doms, _ := parseDomains(resp, f)
	if len(doms) != 1 || doms[0].Name != "商品域" {
		t.Errorf("围栏解析失败: %+v", doms)
	}
}

// TestExportDomainFacts：事实包导出文件——含包/表/实体清单。
func TestExportDomainFacts(t *testing.T) {
	dir := seedRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	acts := action.New(sqlite.NewRepo(db))
	path := filepath.Join(t.TempDir(), "facts.txt")
	if err := exportDomainFacts(dir, acts, wikiConfig{}, sqlite.NewRepo(db), path); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	s := string(b)
	// fixture 无包/表内容——断言结构标题（导出机制成立）
	for _, want := range []string{"代码静态分析事实", "包清单", "数据表"} {
		if !strings.Contains(s, want) {
			t.Errorf("事实包应含 %q:\n%s", want, s)
		}
	}
}
