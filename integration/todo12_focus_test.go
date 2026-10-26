//go:build integration

package integration

// todo12-9/11 验收：真实源码 → 生产索引 → CLI。
//   - --focus return / --focus NodeStatus 闭合 status 初始化、条件改写、
//     必要 Review 依赖，排除 Audit
//   - mermaid 保留两个赋值、条件与返回（不产生空 alt）
//   - 不存在焦点非零退出；合法但无写入焦点正常返回

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const todo12FocusSource = `package regression

type ReviewInfo struct { DefaultStatus string }
type Handler struct {
	NodeStatus string
	Unused     string
}

func Review(info ReviewInfo) string { return info.DefaultStatus }
func Audit() {}
func Unlock() {}
func Notify() {}

func (h *Handler) Wrap(rejected bool) (string, error) {
	defer Unlock()
	go Notify()
	reviewStatus := Review(ReviewInfo{DefaultStatus: "pending"})
	status := "not_started"
	if rejected && reviewStatus != "" {
		status = "rejected"
	}
	h.NodeStatus = status
	Audit()
	return status, nil
}
`

func setupTodo12FocusRepo(t *testing.T) string {
	t.Helper()
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/codeintel-regression\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wrap.go"), []byte(todo12FocusSource), 0o644); err != nil {
		t.Fatal(err)
	}
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

// focusLabels 从 sequence JSON 收集 assign 标签与 call 标签。
func focusLabels(root map[string]any) (assigns, calls, returns []string) {
	walkSeqJSON(root, func(n map[string]any) {
		kind, _ := n["Kind"].(string)
		label, _ := n["Label"].(string)
		switch kind {
		case "assign":
			assigns = append(assigns, label)
		case "call":
			calls = append(calls, label)
		case "return":
			if vals, ok := n["values"].([]any); ok {
				var parts []string
				for _, v := range vals {
					parts = append(parts, fmtStr(v))
				}
				returns = append(returns, strings.Join(parts, ","))
			}
		}
	})
	return
}

func assertTodo12FocusClosure(t *testing.T, root map[string]any, mode string) {
	t.Helper()
	assigns, calls, _ := focusLabels(root)
	joined := strings.Join(assigns, " | ")
	for _, want := range []string{
		`reviewStatus := Review(ReviewInfo{DefaultStatus: "pending"})`,
		`status := "not_started"`,
		`status = "rejected"`,
		"h.NodeStatus = status",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("%s 应保留依赖赋值 %q，实为 %v", mode, want, assigns)
		}
	}
	for _, c := range calls {
		if c == "Audit" {
			t.Errorf("%s 不得包含无关调用 Audit: %v", mode, calls)
		}
	}
	hasReview := false
	for _, c := range calls {
		if strings.Contains(c, "Review") {
			hasReview = true
		}
	}
	if !hasReview {
		t.Errorf("%s 应保留必要 Review 依赖: %v", mode, calls)
	}
}

func sequenceRaw(t *testing.T, dir, sym string, extra ...string) string {
	t.Helper()
	args := append([]string{"query", "sequence", sym, "--repo", dir, "--code", "--depth", "2"}, extra...)
	code, out := runCLIOut(t, args...)
	if code != 0 {
		t.Fatalf("sequence exit = %d; %s", code, out)
	}
	return out
}

func TestTodo12FocusClosureRealIndex(t *testing.T) {
	dir := setupTodo12FocusRepo(t)
	sym := "symbol:go:example.com/codeintel-regression:(Handler).Wrap"
	for _, focus := range []string{"return", "NodeStatus"} {
		out := sequenceRaw(t, dir, sym, "--focus", focus, "--json")
		var root map[string]any
		if err := json.Unmarshal([]byte(out), &root); err != nil {
			t.Fatalf("%s JSON: %v\n%s", focus, err, out)
		}
		assertTodo12FocusClosure(t, root, focus)
		if focus == "return" {
			_, _, returns := focusLabels(root)
			if len(returns) == 0 {
				t.Errorf("return 焦点应保留 return 节点")
			}
		}
	}
	// 文本与 mermaid 同一切片
	text := sequenceRaw(t, dir, sym, "--focus", "status,return")
	for _, want := range []string{`status := "not_started"`, `status = "rejected"`, "return status"} {
		if !strings.Contains(text, want) {
			t.Errorf("文本应含 %q:\n%s", want, text)
		}
	}
	mermaid := sequenceRaw(t, dir, sym, "--focus", "status,return", "--format", "mermaid")
	for _, want := range []string{`status := "not_started"`, `status = "rejected"`, "alt ", "return status"} {
		if !strings.Contains(mermaid, want) {
			t.Errorf("mermaid 应含 %q:\n%s", want, mermaid)
		}
	}
	if strings.Contains(mermaid, "alt rejected\nalt") {
		t.Errorf("不应出现嵌套空 alt:\n%s", mermaid)
	}
}

func TestTodo12FocusValidationRealIndex(t *testing.T) {
	dir := setupTodo12FocusRepo(t)
	sym := "symbol:go:example.com/codeintel-regression:(Handler).Wrap"
	code, out := runCLIOut(t, "query", "sequence", sym, "--repo", dir, "--code",
		"--depth", "1", "--focus", "No.SuchField")
	if code == 0 {
		t.Errorf("不存在的焦点应非零退出:\n%s", out)
	}
	// 合法但无写入：Unused 字段存在（结构体字段元数据）
	code, out = runCLIOut(t, "query", "sequence", sym, "--repo", dir, "--code",
		"--depth", "1", "--focus", "Unused", "--json")
	if code != 0 {
		t.Errorf("合法但无写入焦点应正常返回（exit=%d）:\n%s", code, out)
	}
}
