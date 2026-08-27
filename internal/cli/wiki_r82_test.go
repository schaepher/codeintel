package cli

// R82 测试：codex --json 解析 / 包结构折叠 / 子域三层图 / 架构图
// 方向 / 接入层服务包识别。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestExtractCodexLastMessage：codex --json JSONL——取最后一条
// agent_message（中间行忽略：思考/工具调用等）。
func TestExtractCodexLastMessage(t *testing.T) {
	raw := `{"type":"thinking","payload":{"content":[{"type":"thinking","text":"let me think"}]}}
{"type":"tool_call","payload":{"arguments":"{}"}}
{"type":"agent_message","payload":{"content":[{"type":"output_text","text":"第一条"}]}}
{"type":"agent_message","payload":{"content":[{"type":"output_text","text":"最终答复"}]}}
`
	if got := extractCodexLastMessage(raw); got != "最终答复" {
		t.Errorf("extractCodexLastMessage = %q; want 最终答复", got)
	}
}

// TestExtractCodexLastMessageEmpty：无 agent_message → 空（回退原文）。
func TestExtractCodexLastMessageEmpty(t *testing.T) {
	raw := `{"type":"thinking","payload":{"content":[]}}
`
	if got := extractCodexLastMessage(raw); got != "" {
		t.Errorf("无 agent_message 应返回空: %q", got)
	}
}

// TestArchForceTB：yaml architecture LR/TD → TB（第一张架构图从上到下）。
func TestArchForceTB(t *testing.T) {
	if got := archForceTB("graph LR\n  A --> B\n"); !strings.Contains(got, "graph TB") || strings.Contains(got, "graph LR") {
		t.Errorf("LR 应转 TB: %q", got)
	}
	if got := archForceTB("graph TD\n  A --> B\n"); !strings.Contains(got, "graph TB") {
		t.Errorf("TD 应转 TB: %q", got)
	}
	if got := archForceTB("graph TB\n  A --> B\n"); got != "graph TB\n  A --> B\n" {
		t.Errorf("TB 保持不变: %q", got)
	}
}

// TestArchCuratedTB：AI 整理架构图从上到下（≥3 有效节点才非空——R42）。
func TestArchCuratedTB(t *testing.T) {
	mods := []*domain.WikiModule{
		{Name: "example.com/m", PkgCalls: []*domain.WikiPkgCall{
			{From: "cli", To: "action", Count: 2},
			{From: "domain", To: "canonicalizer", Count: 1},
		}},
	}
	out := archMermaidCurated(mods)
	if !strings.HasPrefix(out, "graph TB") {
		t.Errorf("AI 整理架构图应 graph TB:\n%s", out)
	}
}

// TestPackagesFoldable：包结构 details 折叠（默认折叠）。
func TestPackagesFoldable(t *testing.T) {
	pkgs := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:m", Kind: domain.KindPackage, Name: "m",
			Properties: map[string]any{"doc_comment": "主包"}},
	}
	out := renderPackagesMD(nil, pkgs)
	if !strings.Contains(out, "<details><summary>") {
		t.Errorf("包应 details 折叠:\n%s", out)
	}
}

// TestEntityDomainSubdomainsThreeLayers：R82——有 subdomains 配置时域内
// 渲染子域分组（子域间图 + 子域内图，不只看超限）。
func TestEntityDomainSubdomainsThreeLayers(t *testing.T) {
	g := go2oStyleGraph()
	rc := &wikiRenderCtx{Diagram: "mermaid", cfg: wikiConfig{Domains: []wikiDomainCfg{
		{Name: "交易域", Packages: []string{"order", "wallet"}, Subdomains: []wikiSubdomainCfg{
			{Name: "交易核心", Packages: []string{"github.com/ixre/go2o/pkg/domain/order"}},
		}},
	}}}
	// 单域（无领域分组）→ 全图超限分支：有 subdomains → 子域分组
	out := renderEntitiesSectionMD(g, rc)
	for _, want := range []string{"实体协作", "子域分组", "交易核心"} {
		if !strings.Contains(out, want) {
			t.Errorf("有 subdomains 应三层渲染，缺 %q:\n%s", want, out)
		}
	}
}
