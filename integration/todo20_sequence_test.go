//go:build integration

package integration

// todo20-9：重复调用同一本地函数——真实 init → CLI 必须保留两个调用
// 位置与目标身份（索引 calls 边按函数对汇总）。

import (
	"encoding/json"
	"strings"
	"testing"
)

// seqJSONNodes 递归收集 JSON 树中的指定 Label 调用节点。
func seqJSONNodes(n map[string]any, label string, out *[]map[string]any) {
	if n == nil {
		return
	}
	if n["Label"] == label && n["Kind"] == "call" {
		*out = append(*out, n)
	}
	for _, key := range []string{"Nodes", "Else"} {
		cs, _ := n[key].([]any)
		for _, c := range cs {
			if m, ok := c.(map[string]any); ok {
				seqJSONNodes(m, label, out)
			}
		}
	}
}

func TestTodo20RepeatedCallSequence(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := writeTodo20Repo(t, todo20Flow)
	const saveID = "symbol:go:example.com/render-comment:Save"
	for _, depth := range []string{"1", "2"} {
		code, out := runCLIOut(t, "query", "sequence", "Submit", "--repo", dir,
			"--code", "--depth", depth, "--evidence", "--json")
		if code != 0 {
			t.Fatalf("depth=%s exit=%d\n%s", depth, code, out)
		}
		var root map[string]any
		if err := json.Unmarshal([]byte(out), &root); err != nil {
			t.Fatalf("depth=%s JSON: %v\n%s", depth, err, out)
		}
		var calls []map[string]any
		seqJSONNodes(root, "Save", &calls)
		if len(calls) != 2 {
			t.Fatalf("depth=%s 应保留两个 Save 调用点，实为 %d\n%s", depth, len(calls), out)
		}
		wantLines := []int{5, 7}
		for i, c := range calls {
			if line := int(c["Line"].(float64)); line != wantLines[i] {
				t.Errorf("depth=%s 第 %d 个 Save 行号=%d，期望 %d", depth, i, line, wantLines[i])
			}
			ev, _ := c["evidence"].(map[string]any)
			if ev == nil || ev["symbol_id"] != saveID {
				t.Errorf("depth=%s 行 %v 目标身份缺失: %+v", depth, c["Line"], ev)
				continue
			}
			if ev["verdict"] == "unverified" {
				t.Errorf("depth=%s 行 %v 已定位目标却标 unverified", depth, c["Line"])
			}
			stop, _ := c["stop"].(map[string]any)
			if depth == "1" {
				if stop == nil || stop["code"] != "depth" {
					t.Errorf("depth=1 行 %v 应因 depth 停止，实为 %+v", c["Line"], stop)
				}
				continue
			}
			if stop != nil && stop["code"] == "unresolved" {
				t.Errorf("depth=2 行 %v 不应 unresolved", c["Line"])
			}
			children, _ := c["Nodes"].([]any)
			if len(children) != 1 {
				t.Fatalf("depth=2 行 %v 应展开一个 return 子节点，实为 %d", c["Line"], len(children))
			}
			ret, _ := children[0].(map[string]any)
			if ret == nil || ret["Kind"] != "return" || ret["Label"] != `return "pending"` {
				t.Errorf("depth=2 行 %v 展开内容不对: %+v", c["Line"], ret)
			}
		}
		meta, _ := root["meta"].(map[string]any)
		diags, _ := meta["diagnostics"].([]any)
		depthCount, unresolved := 0, 0
		for _, d := range diags {
			dm, _ := d.(map[string]any)
			switch dm["code"] {
			case "depth":
				depthCount = int(dm["count"].(float64))
			case "unresolved":
				unresolved++
			}
		}
		if unresolved != 0 {
			t.Errorf("depth=%s 不应有 unresolved 诊断: %+v", depth, diags)
		}
		if depth == "1" && depthCount != 2 {
			t.Errorf("depth=1 应 depth×2，实为 %d: %+v", depthCount, diags)
		}
	}

	// Mermaid 与 JSON 同源：两个证据行都带目标 ID，没有 unresolved 诊断。
	code, m := runCLIOut(t, "query", "sequence", "Submit", "--repo", dir,
		"--code", "--depth", "1", "--evidence", "--format", "mermaid")
	if code != 0 {
		t.Fatalf("mermaid exit=%d", code)
	}
	if strings.Contains(m, "diag unresolved") {
		t.Errorf("mermaid 不应有 unresolved 诊断:\n%s", m)
	}
	if !strings.Contains(m, "diag depth x2") {
		t.Errorf("mermaid 应含 depth×2:\n%s", m)
	}
	if got := strings.Count(m, "id="+saveID); got != 2 {
		t.Errorf("mermaid 应有两条带 Save 目标 ID 的证据，实为 %d:\n%s", got, m)
	}
}
