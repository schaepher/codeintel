package cli

// #0 AI Agent 选择 codex/claude：运行器 + 选择解析 + config 读取。
// 测试先行——resolveAgentWith 纯函数覆盖四通道，runAgentExec 用
// fake CLI 脚本验证真实 exec 形态。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestResolveAgentFlagPriority：--agent 显式 > config > auto。
func TestResolveAgentFlagPriority(t *testing.T) {
	got, err := resolveAgentWith("codex", "claude", true, true)
	if err != nil || got != "codex" {
		t.Errorf("flag=codex → %q, %v; want codex", got, err)
	}
	got, err = resolveAgentWith("claude", "", false, false)
	if err != nil || got != "claude" {
		t.Errorf("flag=claude（无 config/无 CLI）→ %q, %v; want claude（显式优先）", got, err)
	}
}

// TestResolveAgentConfigDefault：无 flag 时 config.yaml 默认生效。
func TestResolveAgentConfigDefault(t *testing.T) {
	got, err := resolveAgentWith("", "codex", false, true)
	if err != nil || got != "codex" {
		t.Errorf("config=codex → %q, %v; want codex", got, err)
	}
	// config 指定但 CLI 未装——不自动降级（用户显式配置应报错让安装）
	got, err = resolveAgentWith("", "codex", true, false)
	if err != nil || got != "codex" {
		t.Errorf("config=codex 但 codex 未装 → %q, %v; want codex（配置即意图）", got, err)
	}
}

// TestResolveAgentAuto：auto 时 claude 优先、codex 兜底、都没有报错。
func TestResolveAgentAuto(t *testing.T) {
	got, err := resolveAgentWith("", "", true, true)
	if err != nil || got != "claude" {
		t.Errorf("都可用 → %q, %v; want claude（优先）", got, err)
	}
	got, err = resolveAgentWith("", "", false, true)
	if err != nil || got != "codex" {
		t.Errorf("仅 codex → %q, %v; want codex", got, err)
	}
	_, err = resolveAgentWith("", "", false, false)
	if err == nil || !strings.Contains(err.Error(), "claude") || !strings.Contains(err.Error(), "codex") {
		t.Errorf("都未装 → err=%v; want 报错含两安装提示", err)
	}
}

// TestAgentFromConfig：~/.codeintel/config.yaml 读取（agent 键）。
// R58：无文件时自动初始化（模板默认 agent: auto）——不再返回空。
func TestAgentFromConfig(t *testing.T) {
	dir := t.TempDir()
	old := agentConfigPath
	agentConfigPath = func() string { return filepath.Join(dir, "config.yaml") }
	defer func() { agentConfigPath = old }()
	if got := agentFromConfig(); got != "auto" {
		t.Fatalf("无文件 → %q; want auto（自动初始化模板默认值）", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.yaml")); err != nil {
		t.Errorf("无文件时应自动创建 config.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("agent: codex\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := agentFromConfig(); got != "codex" {
		t.Errorf("config.yaml agent: codex → %q; want codex", got)
	}
}

// TestRunAgentExecClaude：fake claude CLI 脚本——真实 exec 形态
// （claude -p <prompt> --output-format json），JSON 输出提取 result。
func TestRunAgentExecClaude(t *testing.T) {
	claudeSessionID = "" // 重置——防 resume 测试污染 argv 断言
	marker := filepath.Join(t.TempDir(), "argv")
	fakeAgentBin(t, "claude", `#!/bin/sh
s=""
for a in "$@"; do s="$s $a"; done
printf '%s' "${s# }" > `+marker+`
echo '{"result":"fake-response-from-claude"}'
`)
	out, err := runAgentExec("claude", "hello", 5*time.Second, "")
	if err != nil {
		t.Fatalf("runAgentExec: %v", err)
	}
	if out != "fake-response-from-claude" {
		t.Errorf("输出 = %q; want fake-response-from-claude（JSON result 提取）", out)
	}
	b, _ := os.ReadFile(marker)
	if string(b) != `-p hello --output-format json` {
		t.Errorf("claude 实参 = %q; want \"-p hello --output-format json\"", b)
	}
}

// TestRunAgentExecClaudeResume：JSON 输出带 session_id → 后续调用
// 同会话（--resume），不每次开新会话。
func TestRunAgentExecClaudeResume(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "argv2")
	fakeAgentBin(t, "claude", `#!/bin/sh
echo "$@" >> `+marker+`
echo '{"result":"r1","session_id":"sess-abc"}'
`)
	if _, err := runAgentExec("claude", "first", 5*time.Second, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := runAgentExec("claude", "second", 5*time.Second, ""); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(marker)
	if !strings.Contains(string(b), "--resume sess-abc") {
		t.Errorf("第二次调用应带 --resume sess-abc:\n%s", b)
	}
	if strings.Contains(strings.Split(string(b), "\n")[0], "--resume") {
		t.Errorf("首次调用不应带 --resume:\n%s", b)
	}
}

// TestRunAgentExecClaudePlainFallback：旧版 claude（非 JSON 输出）→
// 回退原文。
func TestRunAgentExecClaudePlainFallback(t *testing.T) {
	fakeAgentBin(t, "claude", `#!/bin/sh
echo "plain-text-response"
`)
	out, err := runAgentExec("claude", "hello", 5*time.Second, "")
	if err != nil {
		t.Fatalf("runAgentExec: %v", err)
	}
	if out != "plain-text-response" {
		t.Errorf("输出 = %q; want plain-text-response（非 JSON 回退）", out)
	}
}

// TestRunAgentExecCodex：codex exec <prompt> 形态。
func TestRunAgentExecCodex(t *testing.T) {
	// 记录调用参数：codex 应收到 "exec" + prompt
	marker := filepath.Join(t.TempDir(), "argv")
	fakeAgentBin(t, "codex", `#!/bin/sh
s=""
for a in "$@"; do s="$s $a"; done
printf '%s' "${s# }" > `+marker+`
echo "codex-ok"
`)
	out, err := runAgentExec("codex", "hi", 5*time.Second, "")
	if err != nil || out != "codex-ok" {
		t.Fatalf("runAgentExec(codex) = %q, %v", out, err)
	}
	b, _ := os.ReadFile(marker)
	if string(b) != "exec hi" {
		t.Errorf("codex 实参 = %q; want \"exec hi\"", b)
	}
}

// TestRunAgentExecMissing：CLI 未装 → 明确报错（PATH 隔离，防真实
// claude 被调用）。
func TestRunAgentExecMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := runAgentExec("claude", "hi", 2*time.Second, "")
	if err == nil || !strings.Contains(err.Error(), "claude") {
		t.Errorf("未装 claude → err=%v; want 含 claude", err)
	}
}

// TestRunAgentExecTimeout：超时中止（fake CLI exec sleep——必须 exec
// 替换进程，否则 sleep 子进程持有 stdout 管道，杀 sh 不回收管道）。
func TestRunAgentExecTimeout(t *testing.T) {
	fakeAgentBin(t, "claude", `#!/bin/sh
exec sleep 3
`)
	_, err := runAgentExec("claude", "hi", 200*time.Millisecond, "")
	if err == nil || !strings.Contains(err.Error(), "超时") {
		t.Errorf("超时 → err=%v; want 超时报错", err)
	}
}

// fakeAgentBin 在临时目录写可执行脚本并加入 PATH 前部。
func fakeAgentBin(t *testing.T, name, script string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}
