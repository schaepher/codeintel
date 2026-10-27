//go:build integration

package integration

// todo22 §1/§9.3：分段验收（上一段终点 == 下一段根 ID、场景一致、路径无
// ambiguous/unverified）。从 todo22_flow_matrix_render_test.go 拆出。

import (
	"encoding/json"
	"testing"
)

// seqPathTo 先序查找 evidence.symbol_id == sym 的节点，返回根到该节点的路径。
func seqPathTo(n map[string]any, sym string, path []map[string]any) []map[string]any {
	if n == nil {
		return nil
	}
	path = append(path, n)
	if ev, _ := n["evidence"].(map[string]any); ev != nil && ev["symbol_id"] == sym {
		return path
	}
	for _, key := range []string{"Nodes", "Else"} {
		cs, _ := n[key].([]any)
		for _, c := range cs {
			if m, ok := c.(map[string]any); ok {
				if got := seqPathTo(m, sym, path); got != nil {
					return got
				}
			}
		}
	}
	return nil
}

// TestTodo22SegmentedFlow §1 的分段验收：上一段确定终点 == 下一段根 canonical
// ID、场景一致、祖先没有 ambiguous/unverified。
func TestTodo22SegmentedFlow(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	bin, dir := flowMatrixCLI(t)
	const txSave = "symbol:go:example.com/flowmatrix:(App).txSaveAccount"
	base := []string{"query", "sequence", flowMatrixRenderTarget, "--repo", dir,
		"--code", "--depth", "24", "--scene", "Source=19"}

	// 第一段：入口 → 事务/存储写入方法（分段终点）
	code, out := flowRun(t, bin, append(append([]string{}, base...), "--json")...)
	if code != 0 {
		t.Fatalf("段1 exit=%d\n%s", code, out)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatal(err)
	}
	path := seqPathTo(root, txSave, nil)
	if path == nil {
		t.Fatalf("段1 未到达分段终点 %s", txSave)
	}
	// 祖先的歧义/未验证检查（分段连接必须是确定的）
	for i, n := range path {
		ev, _ := n["evidence"].(map[string]any)
		if ev == nil {
			continue
		}
		if amb, _ := ev["ambiguous"].(bool); amb {
			t.Errorf("段1 路径第 %d 层证据 ambiguous（%v）", i, ev["symbol_id"])
		}
		if v, _ := ev["verdict"].(string); v == "unverified" {
			t.Errorf("段1 路径第 %d 层证据 unverified（%v）", i, ev["symbol_id"])
		}
	}
	endpoint, _ := path[len(path)-1]["evidence"].(map[string]any)
	endID, _ := endpoint["symbol_id"].(string)

	// 第二段：以第一段终点为根（同一场景）
	code, out2 := flowRun(t, bin, "query", "sequence", endID, "--repo", dir,
		"--code", "--depth", "24", "--scene", "Source=19", "--json")
	if code != 0 {
		t.Fatalf("段2 exit=%d\n%s", code, out2)
	}
	var root2 map[string]any
	if err := json.Unmarshal([]byte(out2), &root2); err != nil {
		t.Fatal(err)
	}
	rootEv, _ := root2["evidence"].(map[string]any)
	rootID, _ := rootEv["symbol_id"].(string)
	if rootID != endID {
		t.Errorf("段2 根 ID 与段1 终点不一致: %q vs %q", rootID, endID)
	}
	// 第二段证明主数据写入（DI/AOP → 实现 → SQL）
	for _, want := range []string{
		"symbol:go:example.com/flowmatrix:(storeAOP).SaveAccount",
		"symbol:go:example.com/flowmatrix:(Store).SaveAccount",
		"symbol:go:database/sql:(DB).ExecContext",
	} {
		if seqPathTo(root2, want, nil) == nil {
			t.Errorf("段2 缺少 %s", want)
		}
	}
	// 第三段：状态更新（同一场景、独立入口）
	const txState = "symbol:go:example.com/flowmatrix:(App).txUpdateEntityState"
	if seqPathTo(root, txState, nil) == nil {
		t.Fatalf("段1 未到达状态更新分段终点 %s", txState)
	}
	code, out3 := flowRun(t, bin, "query", "sequence", txState, "--repo", dir,
		"--code", "--depth", "24", "--scene", "Source=19", "--json")
	if code != 0 {
		t.Fatalf("段3 exit=%d\n%s", code, out3)
	}
	var root3 map[string]any
	if err := json.Unmarshal([]byte(out3), &root3); err != nil {
		t.Fatal(err)
	}
	if ev, _ := root3["evidence"].(map[string]any); ev["symbol_id"] != txState {
		t.Errorf("段3 根 ID = %v，期望 %s", ev["symbol_id"], txState)
	}
	if seqPathTo(root3, "symbol:go:example.com/flowmatrix:(Store).UpdateEntityState", nil) == nil {
		t.Errorf("段3 缺少状态更新实现")
	}
}
