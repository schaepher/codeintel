package cli

// R80 subdomains 测试（从 domains_test.go 拆出——行数治理）：AI 输出
// subdomains 解析保留 + 校验 + setDomain 写回嵌套结构。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestParseDomainsSubdomains：R80——AI 输出 subdomains（嵌套
// name/description/packages/tables）→ 解析保留 + 校验（编造包剔除、
// 无归属子域剔除）。
func TestParseDomainsSubdomains(t *testing.T) {
	f := &domainFacts{
		Pkgs:   []pkgFacts{{Path: "item"}, {Path: "order"}, {Path: "member"}},
		Tables: []tableFacts{{Name: "item_info"}, {Name: "order_tab"}},
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
	doms, warns := parseDomains(resp, f)
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

// TestYAMLEditorSetDomainSubdomains：R80——setDomain 写回保留
// subdomains 嵌套结构（重新解析验证）。
func TestYAMLEditorSetDomainSubdomains(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wiki.yaml")
	if err := os.WriteFile(path, []byte("project: {description: test}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := loadYAMLEditor(path)
	if err != nil {
		t.Fatal(err)
	}
	e.setDomain(wikiDomainCfg{
		Name:     "商品域",
		Packages: []string{"item"},
		Subdomains: []wikiSubdomainCfg{
			{Name: "商品核心", Description: "SKU/类目", Packages: []string{"item"}},
		},
	})
	if err := e.save(path); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "subdomains:") || !strings.Contains(string(b), "商品核心") {
		t.Errorf("写回应含 subdomains 嵌套结构:\n%s", b)
	}
	var cfg wikiConfig
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Domains) != 1 || len(cfg.Domains[0].Subdomains) != 1 ||
		cfg.Domains[0].Subdomains[0].Name != "商品核心" {
		t.Errorf("重解析 subdomains = %+v", cfg.Domains)
	}
}
