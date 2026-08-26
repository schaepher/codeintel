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

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
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
	if aiEnabled("fill", wikiConfig{AI: wikiAICfg{Fill: wikiAIFillCfg{"": "off"}}}) {
		t.Error("wiki.yaml ai.fill=off 应禁用")
	}
	// 未知值视为启用（保守）
	if !aiEnabled("ask", wikiConfig{AI: wikiAICfg{Ask: "yes"}}) {
		t.Error("非 off 值应启用")
	}
	// R57：fill 细分（总开关 off 兜底 + 类别级 off）
	if aiEnabled("fill", wikiConfig{AI: wikiAICfg{Fill: wikiAIFillCfg{"": "off"}}}) {
		t.Error("fill: off（总开关）应禁用")
	}
	if aiEnabled("fill.modules", wikiConfig{AI: wikiAICfg{Fill: wikiAIFillCfg{"": "off"}}}) {
		t.Error("fill 总开关 off 应禁用所有类别")
	}
	if aiEnabled("fill.columns", wikiConfig{AI: wikiAICfg{Fill: wikiAIFillCfg{"columns": "off"}}}) {
		t.Error("fill.columns=off 应禁用列补缺")
	}
	if !aiEnabled("fill.modules", wikiConfig{AI: wikiAICfg{Fill: wikiAIFillCfg{"columns": "off"}}}) {
		t.Error("只关 columns——modules 应保持启用")
	}
	if aiEnabled("fill.glossary", wikiConfig{AI: wikiAICfg{Fill: wikiAIFillCfg{"glossary": "off"}}}) {
		t.Error("fill.glossary=off 应禁用术语补缺")
	}
}

// TestWikiFillPartial：ai.fill.columns=off → 列说明补缺跳过（模块/表/
// 术语照常）。
func TestWikiFillPartial(t *testing.T) {
	data, cfg, cols := aiFixtureData()
	cfg.AI = wikiAICfg{Fill: wikiAIFillCfg{"columns": "off"}}
	dir := t.TempDir()
	path := filepath.Join(dir, "wiki.yaml")
	if err := os.WriteFile(path, []byte("project:\n  description: 项目\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gotPrompt := ""
	restore := injectRunner(t, func(agent, prompt string, timeout time.Duration, dir string) (string, error) {
		gotPrompt = prompt
		return aiBatchYAML, nil
	})
	defer restore()
	ok, skip, fail := wikiAIFill(path, &cfg, data, cols, nil, "claude", 30*time.Second, false, nil, "")
	// 模块 1 + 表 1 + 术语 1（列 2 被跳过）
	if ok != 3 || skip != 0 || fail != 0 {
		t.Fatalf("计数 = %d/%d/%d; want 3/0/0（列补缺跳过）", ok, skip, fail)
	}
	if strings.Contains(gotPrompt, "三、表列中文说明") {
		t.Error("columns off 时 prompt 不应含列说明段")
	}
	if !strings.Contains(gotPrompt, "一、模块职责描述") {
		t.Error("columns off 不影响模块段")
	}
}

// TestWikiFillGlossaryOff：ai.fill.glossary=off → prompt 不带术语表段。
func TestWikiFillGlossaryOff(t *testing.T) {
	data, cfg, cols := aiFixtureData()
	cfg.AI = wikiAICfg{Fill: wikiAIFillCfg{"glossary": "off"}}
	dir := t.TempDir()
	path := filepath.Join(dir, "wiki.yaml")
	if err := os.WriteFile(path, []byte("project:\n  description: 项目\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gotPrompt := ""
	restore := injectRunner(t, func(agent, prompt string, timeout time.Duration, dir string) (string, error) {
		gotPrompt = prompt
		return aiBatchYAML, nil
	})
	defer restore()
	ok, _, _ := wikiAIFill(path, &cfg, data, cols, nil, "claude", 30*time.Second, false, nil, "")
	// 模块 1 + 表 1 + 列 2（术语跳过）
	if ok != 4 {
		t.Fatalf("计数 = %d; want 4（术语跳过）", ok)
	}
	if strings.Contains(gotPrompt, "四、术语表") {
		t.Error("glossary off 时 prompt 不应含术语表段")
	}
}

// TestWikiNoDomains：wiki.yaml 未配置 domains → cmdWiki 拒绝生成
// （R57：不允许继续往下走——不再自动调 AI）。
func TestWikiNoDomains(t *testing.T) {
	dir := seedRoutesProcRepo(t)
	if err := os.Remove(filepath.Join(dir, "wiki.yaml")); err != nil {
		t.Fatal(err)
	}
	called := false
	restore := injectRunner(t, func(agent, prompt string, timeout time.Duration, dir string) (string, error) {
		called = true
		return "", nil
	})
	defer restore()
	out := filepath.Join(t.TempDir(), "wiki")
	var code int
	captureStdout(func() {
		code = cmdWiki([]string{"--repo", dir, "--out", out})
	})
	if code == 0 {
		t.Error("无 domains 时 cmdWiki 应拒绝生成（非 0）")
	}
	if called {
		t.Error("无 domains 时不应调用 agentRunner（不自动分析）")
	}
}

// TestWikiFillOff：wiki.yaml ai.fill=off → cmdWiki --ai 跳过补缺步骤
// （不调 resolveAgent/agentRunner）。
func TestWikiFillOff(t *testing.T) {
	dir := seedRoutesProcRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "wiki.yaml"),
		[]byte("domains:\n  - name: 测试域\n    packages: [example.com/m]\nai:\n  fill: off\n"), 0o644); err != nil {
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

// TestCmdDomainsPrompt：domains 命令 --prompt 用户约束传递进
// domainPrompt（R57：wiki 不再自动分析——约束在 domains 命令用）。
func TestCmdDomainsPrompt(t *testing.T) {
	dir := seedRoutesProcRepo(t)
	// 事实包需有包归属（parseDomains 校验：无有效归属的域被剔除）——
	// seed 默认无 package 节点，补一个
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := sqlite.NewRepo(db).SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:pkg", Kind: domain.KindPackage, Name: "pkg"},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	gotPrompt := ""
	restore := injectRunner(t, func(agent, prompt string, timeout time.Duration, dir string) (string, error) {
		gotPrompt = prompt
		return "domains:\n  - name: 交易域\n    description: 交易相关\n    packages: [example.com/m]\n", nil
	})
	defer restore()
	var code int
	captureStdout(func() {
		code = cmdDomainsArgs([]string{"--repo", dir, "--agent", "claude", "--prompt", "订单域：交易域，库存域",
			"--yaml", filepath.Join(t.TempDir(), "wiki.yaml")})
	})
	if code != 0 {
		t.Fatalf("cmdDomains = %d; want 0", code)
	}
	if gotPrompt == "" {
		t.Fatal("domains 应调用 agentRunner")
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

// injectAgentConfigPath 替换 agentConfigPath（全局配置读取注入）。
func injectAgentConfigPath(t *testing.T, path string) func() {
	t.Helper()
	old := agentConfigPath
	agentConfigPath = func() string { return path }
	return func() { agentConfigPath = old }
}
