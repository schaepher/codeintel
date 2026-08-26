package cli

// R77 测试：MCP wiki 特性工具端到端（tools/list 注册 + tools/call
// 调用——packages/architecture/er/processes/module，数据函数与
// query 命令同源）。

import (
	"context"
	"strings"
	"testing"
)

// TestMCPWikiToolsList：tools/list 含 5 个新工具 + inputSchema。
func TestMCPWikiToolsList(t *testing.T) {
	cs := mcpDial(t, seedRepo(t))
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := map[string]bool{}
	for _, tool := range res.Tools {
		names[tool.Name] = true
		if tool.InputSchema == nil {
			t.Errorf("工具 %s 缺 inputSchema", tool.Name)
		}
	}
	for _, want := range []string{"packages", "architecture", "er", "processes", "module"} {
		if !names[want] {
			t.Errorf("tools/list 缺 %s（现有 %d 个工具）", want, len(res.Tools))
		}
	}
}

// TestMCPToolPackages：包结构（doc_comment）。
func TestMCPToolPackages(t *testing.T) {
	cs := mcpDial(t, seedWikiRepo(t))
	text, isErr := mcpCallTool(t, cs, "packages", map[string]any{})
	if isErr {
		t.Fatalf("packages isErr: %s", text)
	}
	for _, want := range []string{`"path"`, "主包：业务入口"} {
		if !strings.Contains(text, want) {
			t.Errorf("packages 应含 %q:\n%s", want, text)
		}
	}
}

// TestMCPToolArchitecture：架构图（mermaid 文本）。
func TestMCPToolArchitecture(t *testing.T) {
	cs := mcpDial(t, seedWikiRepo(t))
	text, isErr := mcpCallTool(t, cs, "architecture", map[string]any{})
	if isErr {
		t.Fatalf("architecture isErr: %s", text)
	}
	for _, want := range []string{`"modules"`, `"mermaid"`} {
		if !strings.Contains(text, want) {
			t.Errorf("architecture 应含 %q:\n%s", want, text)
		}
	}
}

// TestMCPToolER：ER 图（erDiagram + 关系明细）。
func TestMCPToolER(t *testing.T) {
	cs := mcpDial(t, seedTablePathFixture(t))
	text, isErr := mcpCallTool(t, cs, "er", map[string]any{})
	if isErr {
		t.Fatalf("er isErr: %s", text)
	}
	for _, want := range []string{`"mermaid"`, "erDiagram", "table_a"} {
		if !strings.Contains(text, want) {
			t.Errorf("er 应含 %q:\n%s", want, text)
		}
	}
}

// TestMCPToolProcesses：系统流程（main 入口）。
func TestMCPToolProcesses(t *testing.T) {
	cs := mcpDial(t, seedProcessesFixture(t))
	text, isErr := mcpCallTool(t, cs, "processes", map[string]any{})
	if isErr {
		t.Fatalf("processes isErr: %s", text)
	}
	for _, want := range []string{`"entries"`, "main"} {
		if !strings.Contains(text, want) {
			t.Errorf("processes 应含 %q:\n%s", want, text)
		}
	}
}

// TestMCPToolModule：模块详情（核心符号 + 相关表）。
func TestMCPToolModule(t *testing.T) {
	cs := mcpDial(t, seedWikiRepo(t))
	text, isErr := mcpCallTool(t, cs, "module", map[string]any{"name": "m"})
	if isErr {
		t.Fatalf("module isErr: %s", text)
	}
	for _, want := range []string{`"name"`, "main", "orders"} {
		if !strings.Contains(text, want) {
			t.Errorf("module 应含 %q:\n%s", want, text)
		}
	}
}

// TestMCPToolModuleMissing：模块不存在 → isError。
func TestMCPToolModuleMissing(t *testing.T) {
	cs := mcpDial(t, seedWikiRepo(t))
	text, isErr := mcpCallTool(t, cs, "module", map[string]any{"name": "nope"})
	if !isErr {
		t.Fatalf("module 不存在应 isError: %s", text)
	}
}
