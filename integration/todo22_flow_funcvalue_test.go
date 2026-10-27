//go:build integration

package integration

// todo22 §2（第一增量）真实 CLI 回归：回调只在**实际调用它的位置**展开，
// 未证明的回调值不得伪造内部调用。
//
// 夹具 = todo22 §8 的 example.com/flow（Entry → Execute → tx.Run → 回调 →
// process(inner)）。本轮已实现字面量/IIFE/局部函数值/回调形参绑定；
// 装饰器（变参列表 + 工厂返回函数 + 折叠）还没做——所以循环里
// `process = wrapper(ctx, process)` 之后 process 只能是**未消歧的多候选**
// （"改写后的未知值" 与 "循环未执行的旧值"），展开必须整体标 ambiguous，
// 不能把 Store.Insert 当成已证明的确定链路。

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTodo22FlowFuncValueCallback(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	fixture := writeFlowFixture(t)
	if code, out := runCLIOut(t, "init", "--repo", fixture, "--progress", "none"); code != 0 {
		t.Fatalf("init exit=%d\n%s", code, out)
	}
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/flow:Entry",
		"--repo", fixture, "--code", "--depth", "8", "--json")
	if code != 0 {
		t.Fatalf("sequence exit=%d\n%s", code, out)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("JSON 不可解码: %v\n%s", err, out)
	}

	// ① tx.Run 被展开；tx.Run 内部的 f(ctx) 是**回调绑定**证明的执行
	runs := []map[string]any{}
	seqJSONNodes(root, "tx.Run", &runs)
	if len(runs) != 1 {
		t.Fatalf("应展开 1 个 tx.Run 节点，got %d\n%s", len(runs), out)
	}
	callbacks := []map[string]any{}
	seqJSONNodes(runs[0], "f", &callbacks)
	if len(callbacks) != 1 {
		t.Fatalf("tx.Run 内应有 1 个回调执行节点 f，got %d\n%s", len(callbacks), out)
	}
	ev, _ := callbacks[0]["evidence"].(map[string]any)
	if ev == nil || ev["source"] != "closure" {
		t.Errorf("回调节点证据应来自 closure，got %+v", ev)
	}
	if ev != nil {
		if reason, _ := ev["reason"].(string); !strings.Contains(reason, "callback-binding") {
			t.Errorf("回调证据 reason 应说明绑定依据，got %q", reason)
		}
	}

	// ② 回调内部的 process(inner)：装饰器链未证明 → unknown，不展开内部执行
	procs := []map[string]any{}
	seqJSONNodes(callbacks[0], "process", &procs)
	if len(procs) != 1 {
		t.Fatalf("回调内应有 1 个 process 调用节点，got %d\n%s", len(procs), out)
	}
	pev, _ := procs[0]["evidence"].(map[string]any)
	if pev == nil || pev["ambiguous"] != true {
		t.Errorf("装饰器链未证明时 process 调用必须标 ambiguous（不能当确定结论），got %+v", pev)
	}
	if cands, _ := pev["candidates"].([]any); len(cands) < 2 {
		t.Errorf("多候选应列出全部可能值（旧值与改写后的未知值），got %+v", pev)
	}
	// ③ 若某个候选里出现 store.Insert，它必须挂在 ambiguous 节点之下
	// ——是"分别分析的候选"，不是已证明的确定链路（变参绑定/工厂折叠
	// 完成后 §8 的期望表才要求它成为确定链路）。
	inserts := []map[string]any{}
	seqJSONNodes(procs[0], "store.Insert", &inserts)
	if len(inserts) > 0 && pev["ambiguous"] != true {
		t.Errorf("store.Insert 只能作为未消歧候选出现（父 process 节点未标 ambiguous）")
	}
}
