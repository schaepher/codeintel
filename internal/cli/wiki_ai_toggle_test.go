package cli

// R56 AI 使用点配置开关测试：ai.domains/ai.fill/ai.ask（wiki.yaml 仓库级
// > ~/.codeintel/config.yaml 全局 > 默认 auto）；wiki --prompt 用户约束
// 传递。测试先行。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAIEnabled：开关判定——默认启用；全局 off；wiki.yaml 优先。
func TestAIEnabled(t *testing.T) {
	// 全局 config.yaml 注入临时文件
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	restore := injectAgentConfigPath(t, cfgPath)
	defer restore()
	// 无全局配置 → 默认启用
	if !aiEnabled("domains", wikiConfig{}) {
		t.Error("无配置时 domains 应默认启用")
	}
	// 全局 off
	if err := os.WriteFile(cfgPath, []byte("ai:\n  domains: off\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if aiEnabled("domains", wikiConfig{}) {
		t.Error("全局 ai.domains=off 应禁用")
	}
	if !aiEnabled("fill", wikiConfig{}) {
		t.Error("全局只关 domains——fill 应保持启用")
	}
	// wiki.yaml 优先（覆盖全局）
	if !aiEnabled("domains", wikiConfig{AI: wikiAICfg{Domains: "auto"}}) {
		t.Error("wiki.yaml ai.domains=auto 应覆盖全局 off——启用")
	}
	if aiEnabled("fill", wikiConfig{AI: wikiAICfg{Fill: "off"}}) {
		t.Error("wiki.yaml ai.fill=off 应禁用")
	}
	// 未知值视为启用（保守）
	if !aiEnabled("ask", wikiConfig{AI: wikiAICfg{Ask: "yes"}}) {
		t.Error("非 off 值应启用")
	}
}

// TestWikiDomainsOff：wiki.yaml ai.domains=off 且无 domains → cmdWiki
// 不触发 AI 业务域分析（整步跳过，wiki 仍生成）。
func TestWikiDomainsOff(t *testing.T) {
	dir := seedRoutesProcRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "wiki.yaml"),
		[]byte("ai:\n  domains: off\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	called := false
	restore := injectRunner(t, func(agent, prompt string, timeout time.Duration, dir string) (string, error) {
		called = true
		return "", nil
	})
	defer restore()
	out := filepath.Join(t.TempDir(), "wiki")
	if code := cmdWiki([]string{"--repo", dir, "--out", out}); code != 0 {
		t.Fatalf("cmdWiki = %d; want 0", code)
	}
	if called {
		t.Error("ai.domains=off 时不应调用 agentRunner")
	}
}

// TestWikiFillOff：wiki.yaml ai.fill=off → cmdWiki --ai 跳过补缺步骤
// （不调 resolveAgent/agentRunner）。
func TestWikiFillOff(t *testing.T) {
	dir := seedRoutesProcRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "wiki.yaml"),
		[]byte("ai:\n  fill: off\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	called := false
	restore := injectRunner(t, func(agent, prompt string, timeout time.Duration, dir string) (string, error) {
		called = true
		return "", nil
	})
	defer restore()
	out := filepath.Join(t.TempDir(), "wiki")
	if code := cmdWiki([]string{"--repo", dir, "--out", out, "--ai", "--agent", "claude"}); code != 0 {
		t.Fatalf("cmdWiki --ai = %d; want 0", code)
	}
	if called {
		t.Error("ai.fill=off 时 --ai 不应调用 agentRunner")
	}
}

// TestCmdWikiPrompt：--prompt 用户约束传递进 domains 分析 prompt。
func TestCmdWikiPrompt(t *testing.T) {
	// testmain_test.go 全局设置 CODEINTEL_SKIP_DOMAINS=1（测试环境防真调
	// claude）——本测试要验证自动 domains 分析，需取消
	t.Setenv("CODEINTEL_SKIP_DOMAINS", "")
	dir := seedRoutesProcRepo(t)
	// 无 domains → 触发自动分析；agentRunner 捕获 prompt
	gotPrompt := ""
	restore := injectRunner(t, func(agent, prompt string, timeout time.Duration, dir string) (string, error) {
		gotPrompt = prompt
		return "", nil
	})
	defer restore()
	out := filepath.Join(t.TempDir(), "wiki")
	if code := cmdWiki([]string{"--repo", dir, "--out", out, "--prompt", "订单域：交易域，库存域", "--agent", "claude"}); code != 0 {
		t.Fatalf("cmdWiki = %d; want 0", code)
	}
	if gotPrompt == "" {
		t.Fatal("domains 自动分析应调用 agentRunner")
	}
	for _, want := range []string{"用户额外约束", "订单域：交易域，库存域"} {
		if !strings.Contains(gotPrompt, want) {
			t.Errorf("prompt 应含 %q:\n%s", want, gotPrompt)
		}
	}
}

// TestAskAskOff：wiki.yaml ai.ask=off → cmdAsk 报禁用（不调 agent）。
func TestAskAskOff(t *testing.T) {
	dir := seedAskRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "wiki.yaml"),
		[]byte("ai:\n  ask: off\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	called := false
	restore := injectRunner(t, func(agent, prompt string, timeout time.Duration, dir string) (string, error) {
		called = true
		return "", nil
	})
	defer restore()
	var code int
	captureStdout(func() {
		code = cmdAsk([]string{"问题", "--repo", dir, "--agent", "claude"})
	})
	if code == 0 {
		t.Error("ai.ask=off 时 cmdAsk 应返回非 0")
	}
	if called {
		t.Error("ai.ask=off 时不应调用 agentRunner")
	}
}

// TestDomainPromptExtra：domainPrompt 带 extraPrompt 时含用户约束段。
func TestDomainPromptExtra(t *testing.T) {
	p := domainPrompt("facts.json", "")
	if strings.Contains(p, "用户额外约束") {
		t.Error("无 extraPrompt 时不应含约束段")
	}
	p2 := domainPrompt("facts.json", "商品域：交易域")
	for _, want := range []string{"facts.json", "用户额外约束", "商品域：交易域"} {
		if !strings.Contains(p2, want) {
			t.Errorf("prompt 应含 %q", want)
		}
	}
}

// injectAgentConfigPath 替换 agentConfigPath（全局配置读取注入）。
func injectAgentConfigPath(t *testing.T, path string) func() {
	t.Helper()
	old := agentConfigPath
	agentConfigPath = func() string { return path }
	return func() { agentConfigPath = old }
}
