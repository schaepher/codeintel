package cli

// #0 AI Agent 选择 codex/claude：本地 CLI 子进程运行器（零密钥管理，
// 复用本机已登录会话）+ 选择解析（--agent 参数 > ~/.codeintel/
// config.yaml 默认 > auto 检测）+ 三层降级（CLI 缺失报错/超时中止/
// 失败重试由调用方负责）。

import (
	"context"
	_ "embed"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
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
		// R82：codex --json——默认输出多条信息（思考/工具调用等），
		// 只取最后一条 agent_message（见 extractCodexLastMessage）
		cmd = exec.CommandContext(ctx, "codex", "exec", "--json", prompt)
	default:
		return "", fmt.Errorf("未知 agent %q（支持 claude|codex）", agent)
	}
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		// R72/R73：超时诊断——检测本次调用形态的残留进程（claude -p
		// 单次调用），**只报告不 kill**（pgrep 按名匹配会误杀
		// cc-connect 长驻会话进程——实测超时 kill 掉会话 claude 导致
		// 执行中断/重新要权限）。ctx 取消已终止子进程；残留信息供
		// 调用方决定重试/resume/检查输出文件
		left := agentProcesses(agent)
		if len(left) > 0 {
			return "", fmt.Errorf("agent %s 超时（超过 %s）——检测到 %d 个本调用形态残留进程（AI 可能仍在思考，勿手动 kill 会话进程）；若 AI 已写完输出文件可直接使用", agent, timeout, len(left))
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
	if agent == "codex" {
		// R82：codex --json 输出 JSONL——取最后一条 agent_message
		if r := extractCodexLastMessage(raw); r != "" {
			return r, nil
		}
	}
	return raw, nil
}
