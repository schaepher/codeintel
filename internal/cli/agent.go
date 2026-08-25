package cli

// #0 AI Agent 选择 codex/claude：本地 CLI 子进程运行器（零密钥管理，
// 复用本机已登录会话）+ 选择解析（--agent 参数 > ~/.codeintel/
// config.yaml 默认 > auto 检测）+ 三层降级（CLI 缺失报错/超时中止/
// 失败重试由调用方负责）。

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

//go:embed config_example.yaml
var configExample string // 全局配置模板（含全部选项 + 默认值 + 注释；
// 仓库根 config.yaml.example 为同步副本）

// agentRunner 实际 agent 调用（可注入——测试替换）。dir = 子进程工作
// 目录（目标仓库根——R38：claude/codex 对 cwd 项目内文件 Read 免权限
// 弹窗，事实包放仓库 .codeintel/ 内才能被 agent 读取）。
var agentRunner = func(agent, prompt string, timeout time.Duration, dir string) (string, error) {
	return runAgentExec(agent, prompt, timeout, dir)
}

// claudeSessionID 上次 claude 调用的会话 ID——同会话复用（--ai 分批 /
// ask 多轮不每次开新会话；claude -p JSON 输出带 session_id）。
// serve 对话界面多请求并发访问——互斥保护。
var (
	claudeSessionMu sync.Mutex
	claudeSessionID string
)

// runAgentExec 本地 CLI 调用：claude -p --output-format json [--resume
// <会话>] / codex exec <prompt>，捕获 stdout；超时中止；CLI 缺失报错。
// claude 用 JSON 输出模式（-p 为前提）：返回 {result, session_id}，
// 提取 result 返回并记录会话 ID；输出非 JSON（旧版 CLI）时回退原文。
// dir：子进程工作目录（目标仓库根——cwd 项目内文件 Read 免权限）。
func runAgentExec(agent, prompt string, timeout time.Duration, dir string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var cmd *exec.Cmd
	switch agent {
	case "claude":
		args := []string{"-p", prompt, "--output-format", "json"}
		claudeSessionMu.Lock()
		sid := claudeSessionID
		claudeSessionMu.Unlock()
		if sid != "" {
			args = append(args, "--resume", sid)
		}
		cmd = exec.CommandContext(ctx, "claude", args...)
	case "codex":
		cmd = exec.CommandContext(ctx, "codex", "exec", prompt)
	default:
		return "", fmt.Errorf("未知 agent %q（支持 claude|codex）", agent)
	}
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		// R72：超时诊断——检测残留进程（AI 可能仍在思考/卡住），
		// 残留则终止（防孤儿）；错误信息带进程状态——调用方据此
		// 决定重试/resume/检查输出文件，而非盲目完整重试
		left := agentProcesses(agent)
		if len(left) > 0 {
			for _, p := range left {
				_ = p.Kill()
			}
			return "", fmt.Errorf("agent %s 超时（超过 %s）——已终止 %d 个残留进程（AI 可能仍在思考）；若 AI 已写完输出文件可直接使用", agent, timeout, len(left))
		}
		return "", fmt.Errorf("agent %s 超时（超过 %s）——无残留进程（进程已退出）；检查输出文件是否已写入", agent, timeout)
	}
	if err != nil {
		if strings.Contains(err.Error(), "executable file not found") {
			return "", fmt.Errorf("未安装 %s CLI：%s", agent, agentInstallHint(agent))
		}
		return "", fmt.Errorf("agent %s 执行失败: %v\n%s", agent, err, out)
	}
	raw := strings.TrimSpace(string(out))
	if agent == "claude" {
		if r, sid, err := claudeResult(raw); err == nil {
			if sid != "" {
				claudeSessionMu.Lock()
				claudeSessionID = sid
				claudeSessionMu.Unlock()
			}
			return r, nil
		}
		// 非 JSON 输出（旧版 CLI）——回退原文
	}
	return raw, nil
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

// agentProcesses 检测 agent CLI 残留进程（R72 超时诊断——pgrep 精确
// 进程名，防误杀自身；输出为空 = 无残留）。
func agentProcesses(agent string) []*os.Process {
	cmd := exec.Command("pgrep", "-x", agent)
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
