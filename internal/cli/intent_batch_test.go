package cli

// Q244 batch：批量符号概览（文本/JSON）。

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestBatchCLI：batch 多符号文本输出。
func TestBatchCLI(t *testing.T) {
	dir := seedFieldTrace(t)
	out := captureStdout(func() {
		if code := cmdBatch([]string{"main", "nope_nope", "--repo", dir}); code != 0 {
			t.Errorf("batch exit = %d", code)
		}
	})
	if !strings.Contains(out, "main") || !strings.Contains(out, "1/2") {
		t.Errorf("batch 应输出命中数与符号:\n%s", out)
	}
}

// TestBatchJSON：batch --json 契约（results 数组）。
func TestBatchJSON(t *testing.T) {
	dir := seedFieldTrace(t)
	out := captureStdout(func() {
		if code := cmdBatch([]string{"main", "--repo", dir, "--json"}); code != 0 {
			t.Errorf("batch exit = %d", code)
		}
	})
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	results, ok := m["results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("results 应含 1 条: %v", m)
	}
	first := results[0].(map[string]any)
	if first["name"] != "main" {
		t.Errorf("name = %v", first["name"])
	}
}
