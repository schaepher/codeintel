package action

// R34 domains 解析/写回测试（批次 C 随迁自 cli/domains_test.go +
// domains_sub_test.go）：AI 返回解析 + 校验（编造归属剔除）、
// yamlEditor 写回。测试先行。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestParseDomainsValidate：AI 返回含编造包/表 → 剔除 + 警告；有效
// 归属保留。
func TestParseDomainsValidate(t *testing.T) {
	f := &DomainFacts{
		Pkgs:   []PkgFacts{{Path: "item", Doc: "商品"}, {Path: "order", Doc: "订单"}, {Path: "member", Doc: "会员"}},
		Tables: []TableFacts{{Name: "item_info", Cols: 5}, {Name: "order_tab", Cols: 8}},
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
	doms, warns := ParseDomains(resp, f)
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
	f := &DomainFacts{Pkgs: []PkgFacts{{Path: "item"}}, Tables: []TableFacts{{Name: "item_info"}}}
	resp := "```yaml\ndomains:\n  - name: 商品域\n    packages: [item]\n    tables: [item_info]\n```"
	doms, _ := ParseDomains(resp, f)
	if len(doms) != 1 || doms[0].Name != "商品域" {
		t.Errorf("围栏解析失败: %+v", doms)
	}
}

// TestParseDomainsSubdomains：R80——AI 输出 subdomains（嵌套
// name/description/packages/tables）→ 解析保留 + 校验（编造包剔除、
// 无归属子域剔除）。
func TestParseDomainsSubdomains(t *testing.T) {
	f := &DomainFacts{
		Pkgs:   []PkgFacts{{Path: "item"}, {Path: "order"}, {Path: "member"}},
		Tables: []TableFacts{{Name: "item_info"}, {Name: "order_tab"}},
	}
	resp := `domains:
  - name: 商品域
    packages: [item]
    tables: [item_info]
    subdomains:
      - name: 商品核心
        description: SKU/类目
        packages: [item]
        tables: [item_info]
      - name: 幽灵子域
        packages: [ghost_pkg]
      - name: 空子域
`
	doms, warns := ParseDomains(resp, f)
	if len(doms) != 1 {
		t.Fatalf("域数 = %d; want 1", len(doms))
	}
	subs := doms[0].Subdomains
	if len(subs) != 1 || subs[0].Name != "商品核心" {
		t.Fatalf("subdomains = %+v; want [商品核心]（幽灵/空子域剔除）", subs)
	}
	if subs[0].Description != "SKU/类目" || len(subs[0].Packages) != 1 || len(subs[0].Tables) != 1 {
		t.Errorf("商品核心子域 = %+v", subs[0])
	}
	if len(warns) < 3 {
		t.Errorf("警告数 = %d; want ≥3（幽灵子域包/幽灵子域/空子域）:\n%v", len(warns), warns)
	}
}

// TestYAMLEditorSetDomainSubdomains：R80——SetDomain 写回保留
// subdomains 嵌套结构（重新解析验证）。
func TestYAMLEditorSetDomainSubdomains(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wiki.yaml")
	if err := os.WriteFile(path, []byte("project: {description: test}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := LoadYAMLEditor(path)
	if err != nil {
		t.Fatal(err)
	}
	e.SetDomain(WikiDomainCfg{
		Name:     "商品域",
		Packages: []string{"item"},
		Subdomains: []WikiSubdomainCfg{
			{Name: "商品核心", Description: "SKU/类目", Packages: []string{"item"}},
		},
	})
	if err := e.Save(path); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "subdomains:") || !strings.Contains(string(b), "商品核心") {
		t.Errorf("写回应含 subdomains 嵌套结构:\n%s", b)
	}
	var out struct {
		Domains []WikiDomainCfg `yaml:"domains"`
	}
	if err := yaml.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Domains) != 1 || len(out.Domains[0].Subdomains) != 1 ||
		out.Domains[0].Subdomains[0].Name != "商品核心" {
		t.Errorf("重解析 subdomains = %+v", out.Domains)
	}
}
