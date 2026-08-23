package cli

// Q244 意图命令 before/trace：目标形态分派 + 聚合输出（文本/JSON）。

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestBeforeSymbolCLI：before <符号> 文本输出（目标 + 调用者）。
func TestBeforeSymbolCLI(t *testing.T) {
	dir := seedFieldTrace(t)
	out := captureStdout(func() {
		if code := cmdBefore([]string{"main", "--repo", dir}); code != 0 {
			t.Errorf("before exit = %d", code)
		}
	})
	if !strings.Contains(out, "main") || !strings.Contains(out, "symbol") {
		t.Errorf("before 应输出目标类型:\n%s", out)
	}
}

// TestBeforeJSON：before --json 契约（target.kind + 缺省组省略）。
func TestBeforeJSON(t *testing.T) {
	dir := seedFieldTrace(t)
	out := captureStdout(func() {
		if code := cmdBefore([]string{"example.com/m.T.A", "--repo", dir, "--json"}); code != 0 {
			t.Errorf("before exit = %d", code)
		}
	})
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	tgt := m["target"].(map[string]any)
	if tgt["kind"] != "field" {
		t.Errorf("target.kind = %v, want field", tgt["kind"])
	}
	if _, ok := m["callers"]; ok {
		t.Errorf("字段目标不应有 callers 组: %v", m)
	}
	if m["writers"] == nil || m["reads"] == nil {
		t.Errorf("字段目标应含 writers/reads: %v", m)
	}
}

// TestBeforeTableCLI：before <表> 文本输出（表关联）。
func TestBeforeTableCLI(t *testing.T) {
	dir := seedTablePathFixture(t)
	out := captureStdout(func() {
		if code := cmdBefore([]string{"table_a", "--repo", dir}); code != 0 {
			t.Errorf("before exit = %d", code)
		}
	})
	if !strings.Contains(out, "table") || !strings.Contains(out, "关联") {
		t.Errorf("表目标应输出关联:\n%s", out)
	}
}

// TestTraceCLI：trace <字段> 文本输出（值流 + 主链）。
func TestTraceCLI(t *testing.T) {
	dir := seedFieldTrace(t)
	out := captureStdout(func() {
		if code := cmdTrace([]string{"example.com/m.T.A", "--repo", dir}); code != 0 {
			t.Errorf("trace exit = %d", code)
		}
	})
	if !strings.Contains(out, "field") || !strings.Contains(out, "值流") {
		t.Errorf("trace 应输出值流链:\n%s", out)
	}
}

// TestTraceJSON：trace --json 契约（target/flows/chain）。
func TestTraceJSON(t *testing.T) {
	dir := seedFieldTrace(t)
	out := captureStdout(func() {
		if code := cmdTrace([]string{"example.com/m.T.A", "--repo", dir, "--json"}); code != 0 {
			t.Errorf("trace exit = %d", code)
		}
	})
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if _, ok := m["flows"]; !ok {
		t.Errorf("trace 应含 flows: %v", m)
	}
}
