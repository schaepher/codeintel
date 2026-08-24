package cli

// #0 AI Agent 选择 codex/claude：本地 CLI 子进程运行器（零密钥管理，
// 复用本机已登录会话）+ 选择解析（--agent 参数 > ~/.codeintel/
// config.yaml 默认 > auto 检测）+ 三层降级（CLI 缺失报错/超时中止/
// 失败重试由调用方负责）。

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// agentRunner 实际 agent 调用（可注入——测试替换）。
var agentRunner = func(agent, prompt string, timeout time.Duration) (string, error) {
	return runAgentExec(agent, prompt, timeout)
}

// runAgentExec 本地 CLI 调用：claude -p --output-format json / codex exec
// <prompt>，捕获 stdout；超时中止；CLI 缺失报错。
// claude 用 JSON 输出模式（-p 为前提）：返回 {result: <文本>}，
// 提取 result 返回；输出非 JSON（旧版 CLI）时回退原文。
func runAgentExec(agent, prompt string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var cmd *exec.Cmd
	switch agent {
	case "claude":
		cmd = exec.CommandContext(ctx, "claude", "-p", prompt, "--output-format", "json")
	case "codex":
		cmd = exec.CommandContext(ctx, "codex", "exec", prompt)
	default:
		return "", fmt.Errorf("未知 agent %q（支持 claude|codex）", agent)
	}
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return "", fmt.Errorf("agent %s 超时（超过 %s）", agent, timeout)
	}
	if err != nil {
		if strings.Contains(err.Error(), "executable file not found") {
			return "", fmt.Errorf("未安装 %s CLI：%s", agent, agentInstallHint(agent))
		}
		return "", fmt.Errorf("agent %s 执行失败: %v\n%s", agent, err, out)
	}
	raw := strings.TrimSpace(string(out))
	if agent == "claude" {
		if r, err := claudeResult(raw); err == nil {
			return r, nil
		}
		// 非 JSON 输出（旧版 CLI）——回退原文
	}
	return raw, nil
}

// claudeResult 从 claude --output-format json 输出提取 result 字段。
func claudeResult(raw string) (string, error) {
	var m struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return "", err
	}
	if m.Result == "" {
		return "", fmt.Errorf("claude JSON 输出缺 result 字段")
	}
	return strings.TrimSpace(m.Result), nil
}

// agentInstallHint 安装提示。
func agentInstallHint(agent string) string {
	switch agent {
	case "codex":
		return "安装: npm install -g @openai/codex"
	default:
		return "安装: https://claude.com/claude-code"
	}
}

// agentConfigPath ~/.codeintel/config.yaml（可覆盖——测试）。
var agentConfigPath = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codeintel", "config.yaml")
}

// agentFromConfig 读全局配置的默认 agent（`agent: claude|codex|auto`）。
func agentFromConfig() string {
	p := agentConfigPath()
	if p == "" {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	var c struct {
		Agent string `yaml:"agent"`
	}
	if err := yaml.Unmarshal(b, &c); err != nil {
		return ""
	}
	return strings.TrimSpace(c.Agent)
}

// resolveAgent --agent 参数 → 生效 agent：显式 > config > auto。
func resolveAgent(flagAgent string) (string, error) {
	cfg := agentFromConfig()
	return resolveAgentWith(flagAgent, cfg, agentAvailable("claude"), agentAvailable("codex"))
}

// resolveAgentWith 纯函数选择逻辑（测试直接覆盖四通道）。
func resolveAgentWith(flagAgent, cfgAgent string, hasClaude, hasCodex bool) (string, error) {
	if flagAgent == "codex" || flagAgent == "claude" {
		return flagAgent, nil // 显式优先，不查可用性（配置即意图）
	}
	if cfgAgent == "codex" || cfgAgent == "claude" {
		return cfgAgent, nil
	}
	if hasClaude {
		return "claude", nil
	}
	if hasCodex {
		return "codex", nil
	}
	return "", fmt.Errorf("未检测到 claude 或 codex CLI——安装其一后重试\n  %s\n  %s",
		agentInstallHint("claude"), agentInstallHint("codex"))
}

// agentAvailable CLI 是否在 PATH。
func agentAvailable(agent string) bool {
	_, err := exec.LookPath(agent)
	return err == nil
}
