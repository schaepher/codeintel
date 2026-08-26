package action

// R9x 测试（迁自 cli/query_wiki_test.go 的逻辑断言部分）：Architecture
// ——模块数聚合 + 业务域 + 架构图文本装配（mermaid fallback 生成是
// cli/wiki 渲染，不入 action）。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestArchitecture：模块数/业务域/架构图文本装配。
func TestArchitecture(t *testing.T) {
	a, _ := seedRepo(t)
	data := []*domain.WikiModule{
		{Name: "example.com/m", ShortName: "m"},
		{Name: "example.com/m/svc", ShortName: "svc"},
	}
	out, err := a.Architecture(ArchitectureRequest{
		Data:    data,
		Domains: []string{"测试域"},
		Mermaid: "flowchart LR\n  A --> B\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Modules != 2 {
		t.Errorf("modules = %d; want 2", out.Modules)
	}
	if len(out.Domains) != 1 || out.Domains[0] != "测试域" {
		t.Errorf("domains = %v", out.Domains)
	}
	if !strings.Contains(out.Mermaid, "flowchart") {
		t.Errorf("mermaid = %q", out.Mermaid)
	}
}

// TestArchitectureEmptyDomains：无 domains 配置时返回空切片（JSON
// 契约稳定）。
func TestArchitectureEmptyDomains(t *testing.T) {
	a, _ := seedRepo(t)
	out, err := a.Architecture(ArchitectureRequest{Data: nil, Mermaid: ""})
	if err != nil {
		t.Fatal(err)
	}
	if out.Modules != 0 {
		t.Errorf("modules = %d; want 0", out.Modules)
	}
	if out.Domains == nil || len(out.Domains) != 0 {
		t.Errorf("domains = %v; want 空切片", out.Domains)
	}
}
