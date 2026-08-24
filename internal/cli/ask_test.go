package cli

// #0 codeintel ask：问题 + 项目上下文打包 → AI 问答（选择 codex/claude）。
// 测试先行——注入 agentRunner 捕获 prompt 验证上下文打包；真实 exec
// 路径由 agent_test 覆盖。

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedAskRepo seedRepo + orders 表列（外部虚拟节点，GetAllTableColumns
// 数据源——ask 自动识别表名的匹配对象）。
func seedAskRepo(t *testing.T) string {
	t.Helper()
	dir := seedRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	var nodes []*domain.CodeEntity
	for i, c := range []struct{ col, typ string }{
		{"orders.id", "INTEGER"}, {"orders.order_no", "TEXT"}, {"orders.created_at", "TEXT"},
	} {
		nodes = append(nodes, &domain.CodeEntity{
			ID:       domain.CanonicalID("symbol:go:example.com/m:main#ext.gorm." + c.col + ".write@" + string(rune('6'+i))),
			Kind:     domain.KindFieldAccess,
			Name:     c.col,
			FilePath: "main.go",
			Properties: map[string]any{"is_external": "true", "type_string": "gorm",
				"col_type": c.typ, "access_kind": "write", "func_id": "symbol:go:example.com/m:main"},
		})
	}
	if _, err := r.SaveBatchStats(nodes, nil, nil); err != nil {
		t.Fatalf("save table cols: %v", err)
	}
	return dir
}

// injectRunner 替换 agentRunner 并返回恢复函数。
func injectRunner(t *testing.T, fn func(agent, prompt string, timeout time.Duration, dir string) (string, error)) func() {
	t.Helper()
	old := agentRunner
	agentRunner = fn
	return func() { agentRunner = old }
}

// TestCmdAskContextPacking：--symbol/--table 显式上下文进 prompt，
// stdout 透传 AI 回答。
func TestCmdAskContextPacking(t *testing.T) {
	dir := seedAskRepo(t)
	gotPrompt := ""
	restore := injectRunner(t, func(agent, prompt string, timeout time.Duration, dir string) (string, error) {
		gotPrompt = prompt
		return "main 调用 (Svc).Run", nil
	})
	defer restore()
	var code int
	out := captureStdout(func() {
		code = cmdAsk([]string{"main 做了什么？", "--symbol", "main", "--table", "orders",
			"--repo", dir, "--agent", "claude"})
	})
	if code != 0 {
		t.Fatalf("cmdAsk = %d; want 0", code)
	}
	for _, want := range []string{
		"=== 符号 main ===",
		"symbol:go:example.com/m:main",
		"=== 表 orders ===",
		"orders.id",
		"(Svc).Run", // 符号上下文里的调用者
	} {
		if !strings.Contains(gotPrompt, want) {
			t.Errorf("prompt 缺 %q:\n%s", want, gotPrompt)
		}
	}
	if !strings.Contains(out, "main 调用 (Svc).Run") {
		t.Errorf("stdout 应含 AI 回答:\n%s", out)
	}
}

// TestCmdAskAutoDetect：问题中直接出现的表名/符号名自动打包（精确匹配）。
func TestCmdAskAutoDetect(t *testing.T) {
	dir := seedAskRepo(t)
	gotPrompt := ""
	restore := injectRunner(t, func(agent, prompt string, timeout time.Duration, dir string) (string, error) {
		gotPrompt = prompt
		return "ok", nil
	})
	defer restore()
	code := cmdAsk([]string{"orders 表有哪些列？", "--repo", dir, "--agent", "claude"})
	if code != 0 {
		t.Fatalf("cmdAsk = %d; want 0", code)
	}
	if !strings.Contains(gotPrompt, "=== 表 orders ===") {
		t.Errorf("自动识别表名失败:\n%s", gotPrompt)
	}
	if !strings.Contains(gotPrompt, "orders.created_at") {
		t.Errorf("表上下文应含全部列:\n%s", gotPrompt)
	}
}

// TestCmdAskNoMatch：问题无符号/表名 → 纯透传（无上下文块）。
func TestCmdAskNoMatch(t *testing.T) {
	dir := seedAskRepo(t)
	gotPrompt := ""
	restore := injectRunner(t, func(agent, prompt string, timeout time.Duration, dir string) (string, error) {
		gotPrompt = prompt
		return "回答", nil
	})
	defer restore()
	code := cmdAsk([]string{"你好", "--repo", dir, "--agent", "claude"})
	if code != 0 {
		t.Fatalf("cmdAsk = %d; want 0", code)
	}
	if strings.Contains(gotPrompt, "===") {
		t.Errorf("无匹配时不应打包上下文:\n%s", gotPrompt)
	}
	if !strings.Contains(gotPrompt, "你好") {
		t.Errorf("prompt 应含原始问题:\n%s", gotPrompt)
	}
}

// TestCmdAskJSON：--json 输出契约 {agent, prompt, response, duration_ms}。
func TestCmdAskJSON(t *testing.T) {
	dir := seedAskRepo(t)
	restore := injectRunner(t, func(agent, prompt string, timeout time.Duration, dir string) (string, error) {
		return "JSON 回答", nil
	})
	defer restore()
	out := captureStdout(func() {
		if code := cmdAsk([]string{"hi", "--repo", dir, "--agent", "codex", "--json"}); code != 0 {
			t.Fatalf("cmdAsk = %d; want 0", code)
		}
	})
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("JSON 解析失败: %v", err)
	}
	if m["agent"] != "codex" || m["response"] != "JSON 回答" {
		t.Errorf("JSON = %v; want agent=codex response=JSON 回答", m)
	}
	if _, ok := m["duration_ms"]; !ok {
		t.Errorf("JSON 缺 duration_ms: %v", m)
	}
}

// TestCmdAskREPL：无问题参数 → 交互模式——多轮追问复用同一会话
// （注入 runner 验证：首轮带符号上下文，追问轮不再重复打包）。
func TestCmdAskREPL(t *testing.T) {
	dir := seedAskRepo(t)
	var prompts []string
	restore := injectRunner(t, func(agent, prompt string, timeout time.Duration, dir string) (string, error) {
		prompts = append(prompts, prompt)
		return "回答" + strconv.Itoa(len(prompts)), nil
	})
	defer restore()
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	_, _ = w.WriteString("main 做什么？\n再详细点\nexit\n")
	_ = w.Close()
	defer func() { os.Stdin = oldStdin }()
	var code int
	out := captureStdout(func() {
		code = cmdAsk([]string{"--repo", dir, "--agent", "claude"})
	})
	if code != 0 {
		t.Fatalf("cmdAsk = %d; want 0", code)
	}
	if len(prompts) != 2 {
		t.Fatalf("REPL 轮数 = %d; want 2（首轮 + 追问）", len(prompts))
	}
	if !strings.Contains(prompts[0], "=== 符号 main ===") {
		t.Errorf("首轮应打包符号上下文:\n%s", prompts[0])
	}
	if strings.Contains(prompts[1], "=== 符号") {
		t.Errorf("追问轮不应重复打包上下文（resume 已带前文）:\n%s", prompts[1])
	}
	if !strings.Contains(out, "回答1") || !strings.Contains(out, "回答2") {
		t.Errorf("stdout 应含两轮回答:\n%s", out)
	}
}

// TestCmdAskCollectsQA：回答成功后 Q&A 收集进 qa_history（W2——wiki
// --with-qa 参考资料）。
func TestCmdAskCollectsQA(t *testing.T) {
	dir := seedAskRepo(t)
	restore := injectRunner(t, func(agent, prompt string, timeout time.Duration, dir string) (string, error) {
		return "main 是入口", nil
	})
	defer restore()
	captureStdout(func() {
		if code := cmdAsk([]string{"main 做什么？", "--repo", dir, "--agent", "claude"}); code != 0 {
			t.Fatalf("cmdAsk = %d; want 0", code)
		}
	})
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	recs, err := sqlite.NewRepo(db).QAForSymbols([]string{"main"}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].Question != "main 做什么？" || recs[0].Answer != "main 是入口" {
		t.Errorf("qa_history = %+v; want 1 条 main 问答", recs)
	}
	if recs[0].Context != "main" {
		t.Errorf("context = %q; want main（相关性匹配键）", recs[0].Context)
	}
}

// TestCmdAskTimeout：--timeout 小于 CLI 执行时长 → 报错退出码非 0。
func TestCmdAskTimeout(t *testing.T) {
	dir := seedAskRepo(t)
	fakeAgentBin(t, "claude", `#!/bin/sh
exec sleep 2
`)
	var code int
	captureStderr(func() {
		code = cmdAsk([]string{"hi", "--repo", dir, "--timeout", "300ms"})
	})
	if code == 0 {
		t.Error("超时应非 0 退出")
	}
}
