package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// extractCodexLastMessage 从 codex --json 输出（JSONL——思考/工具调用/
// agent_message 等事件逐行）提取最后一条 agent_message 的文本。
// codex 默认输出多条信息，真实答复在最后一条 agent_message；
// 无 agent_message 行返回空（调用方回退原文）。
func extractCodexLastMessage(raw string) string {
	last := ""
	for _, l := range strings.Split(raw, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || !strings.HasPrefix(l, "{") {
			continue
		}
		var ev struct {
			Type    string `json:"type"`
			Payload struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"payload"`
		}
		if err := json.Unmarshal([]byte(l), &ev); err != nil {
			continue
		}
		if ev.Type != "agent_message" {
			continue
		}
		text := ""
		for _, c := range ev.Payload.Content {
			if c.Type == "output_text" || c.Type == "text" {
				text += c.Text
			}
		}
		if text != "" {
			last = text
		}
	}
	return last
}

// claudeResult 从 claude --output-format json 输出提取 result 与
// session_id（会话复用）。
func claudeResult(raw string) (string, string, error) {
	var m struct {
		Result    string `json:"result"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return "", "", err
	}
	if m.Result == "" {
		return "", "", fmt.Errorf("claude JSON 输出缺 result 字段")
	}
	return strings.TrimSpace(m.Result), m.SessionID, nil
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

// ensureGlobalConfig 首次运行时自动初始化全局配置（R58）：文件不存在
// → 创建 ~/.codeintel 目录 + 从内置模板复制（含全部选项 + 默认值 +
// 注释）。幂等——已存在（含用户改过的）不覆盖。
func ensureGlobalConfig() {
	p := agentConfigPath()
	if p == "" {
		return
	}
	if _, err := os.Stat(p); err == nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(p, []byte(configExample), 0o644)
}

// agentFromConfig 读全局配置的默认 agent（`agent: claude|codex|auto`）。
func agentFromConfig() string {
	ensureGlobalConfig()
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

// aiConfigFromGlobal 读全局配置的 AI 使用点开关（`ai: {domains, fill,
// ask: auto|off}`）——R56：~/.codeintel/config.yaml，仓库级 wiki.yaml
// 优先（aiEnabled 里合并）。R58：文件不存在时自动初始化（模板）。
func aiConfigFromGlobal() wikiAICfg {
	ensureGlobalConfig()
	p := agentConfigPath()
	if p == "" {
		return wikiAICfg{}
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return wikiAICfg{}
	}
	var c struct {
		AI wikiAICfg `yaml:"ai"`
	}
	if err := yaml.Unmarshal(b, &c); err != nil {
		return wikiAICfg{}
	}
	return c.AI
}

// aiEnabled AI 使用点开关判定（R56）：wiki.yaml ai.<key>（仓库级）>
// ~/.codeintel/config.yaml ai.<key>（全局）> 默认启用。值 off 才禁用
// （其他值/空 = auto 启用）。
// key：domains | fill（总开关）| fill.modules/fill.tables/fill.columns/
// fill.glossary（R57 细分）| ask。
func aiEnabled(key string, cfg wikiConfig) bool {
	val := aiValue(key, cfg.AI)
	if val != "" {
		return val != "off"
	}
	g := aiConfigFromGlobal()
	val = aiValue(key, g)
	if val != "" {
		return val != "off"
	}
	return true
}

// aiValue 取开关值（domains|fill|fill.<类别>|ask）。
func aiValue(key string, c wikiAICfg) string {
	switch {
	case key == "domains":
		return strings.TrimSpace(c.Domains)
	case key == "ask":
		return strings.TrimSpace(c.Ask)
	case key == "fill" || strings.HasPrefix(key, "fill."):
		cat := strings.TrimPrefix(key, "fill.")
		return c.Fill.value(cat)
	}
	return ""
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

// agentProcesses 检测 agent CLI 残留进程（R73 修复：**只匹配本次调用
// 形态**——pgrep -x 按名匹配会误伤 cc-connect 长驻会话进程（实测
// 被杀导致会话中断/重新要权限）。claude 单次调用 = `claude -p ...`；
// codex 单次调用 = `codex exec`。返回 PID 列表供报告，不 kill。
func agentProcesses(agent string) []*os.Process {
	var pattern string
	switch agent {
	case "claude":
		pattern = "[c]laude -p "
	case "codex":
		pattern = "[c]odex exec"
	default:
		return nil
	}
	cmd := exec.Command("pgrep", "-f", pattern)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var procs []*os.Process
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var pid int
		if _, err := fmt.Sscanf(line, "%d", &pid); err == nil && pid > 0 {
			if p, err := os.FindProcess(pid); err == nil {
				procs = append(procs, p)
			}
		}
	}
	return procs
}
