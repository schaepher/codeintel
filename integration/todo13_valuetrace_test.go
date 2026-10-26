//go:build integration

package integration

// todo13-10 A2：未知回调值流不得报 complete（真实索引 → CLI）。
//   - Unsupported 写节点：analysis.status=partial，stop=unknown_callback，
//     说明 f() 未知回调并给出实际文件:行号
//   - Apply 对照仍追到状态常量且 complete
//   - 低深度对照 stop=depth（不与未知回调混同）

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const todo13ValueTraceSrc = `package regression

type State string

const StateApplied State = "applied"

type StateHandler struct{ State State }

func (h *StateHandler) Unsupported(f func() State) State {
	h.State = f()
	return h.State
}

func Apply() State {
	h := &StateHandler{}
	h.State = StateApplied
	return h.State
}
`

func TestTodo13ValueTraceUnknownCallbackRealIndex(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/codeintel-regression\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vt.go"), []byte(todo13ValueTraceSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	base := "symbol:go:example.com/codeintel-regression:"
	writeNode := func(fn string) string {
		t.Helper()
		code, out := runCLIOut(t, "query", "trace-backward",
			"example.com/codeintel-regression.StateHandler.State", "--func", fn,
			"--repo", dir, "--json")
		if code != 0 {
			t.Fatalf("trace-backward exit = %d; %s", code, out)
		}
		var res struct {
			Rows []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
				Line int    `json:"line"`
			} `json:"rows"`
		}
		if err := json.Unmarshal([]byte(out), &res); err != nil {
			t.Fatalf("trace-backward JSON: %v\n%s", err, out)
		}
		for _, r := range res.Rows {
			if strings.Contains(r.ID, ".write@") {
				return r.ID
			}
		}
		t.Fatalf("未找到写节点: %+v", res.Rows)
		return ""
	}
	vt := func(id string, extra ...string) (string, map[string]any) {
		t.Helper()
		args := append([]string{"query", "value-trace", id, "--repo", dir, "--json"}, extra...)
		code, out := runCLIOut(t, args...)
		if code != 0 {
			t.Fatalf("value-trace exit = %d; %s", code, out)
		}
		var res struct {
			Flows    []map[string]any `json:"flows"`
			Analysis map[string]any   `json:"analysis"`
		}
		if err := json.Unmarshal([]byte(out), &res); err != nil {
			t.Fatalf("value-trace JSON: %v\n%s", err, out)
		}
		return out, res.Analysis
	}

	// Unsupported：未知回调 → partial + 原因/位置
	unsupported := writeNode(base + "(StateHandler).Unsupported")
	out, analysis := vt(unsupported, "--max-depth", "8")
	if analysis["status"] != "partial" || analysis["stop"] != "unknown_callback" {
		t.Fatalf("未知回调应 partial/unknown_callback: %+v\n%s", analysis, out)
	}
	detail, _ := analysis["stop_detail"].(string)
	if !strings.Contains(detail, "f") || !strings.Contains(detail, "未知回调") {
		t.Errorf("停止说明应指向 f() 未知回调: %q", detail)
	}
	if f, _ := analysis["stop_file"].(string); f != "vt.go" {
		t.Errorf("停止位置应为实际文件: %+v", analysis)
	}
	if l, _ := analysis["stop_line"].(float64); l <= 0 {
		t.Errorf("停止位置应为正行号: %+v", analysis)
	}
	// 已知值流仍保留
	for _, want := range []string{"h.State", "param.recv.h"} {
		if !strings.Contains(out, want) {
			t.Errorf("应保留已知写入 → h.State 值流（%s）:\n%s", want, out)
		}
	}

	// Apply 对照：追到常量 applied 且 complete
	apply := writeNode(base + "Apply")
	applyOut, applyAnalysis := vt(apply, "--max-depth", "8")
	if applyAnalysis["status"] != "complete" {
		t.Fatalf("Apply 应 complete: %+v\n%s", applyAnalysis, applyOut)
	}
	if !strings.Contains(applyOut, "applied") {
		t.Errorf("Apply 应追到状态常量 applied:\n%s", applyOut)
	}

	// 低深度对照：depth 截断，不与未知回调混同
	_, low := vt(apply, "--max-depth", "1")
	if low["status"] != "partial" || low["stop"] != "depth" {
		t.Errorf("低深度应 stop=depth: %+v", low)
	}

	// 文本同口径
	code, text := runCLIOut(t, "query", "value-trace", unsupported, "--repo", dir, "--max-depth", "8")
	if code != 0 || !strings.Contains(text, "unknown_callback") || !strings.Contains(text, "vt.go:") {
		t.Errorf("文本应说明未知回调及位置: code=%d\n%s", code, text)
	}
}
