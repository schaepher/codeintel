package action

// todo22 §2（第一增量）测试：回调形参绑定 / 未绑定 unknown / 逃逸 / 具名
// 函数值的到达定义。

import (
	"strings"
	"testing"
)

// TestSeqFuncValueCallbackBinding：Entry → Run → 回调 → 回调内部调用。
func TestSeqFuncValueCallbackBinding(t *testing.T) {
	src := `package m

func Delivered() {}

func Run(f func()) { f() }

func Entry() { Run(func() { Delivered() }) }
`
	const pkg = "symbol:go:example.com/m:"
	calls := map[string]string{"Run": pkg + "Run", "Delivered": pkg + "Delivered"}
	acts, dir := seedFuncValueRepo(t, src, calls)
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Entry", RepoAbs: dir, Depth: 4})
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}
	runs := seqCallNodesNamed(root, "Run")
	if len(runs) != 1 {
		t.Fatalf("应有 1 个 Run 调用节点，got %d", len(runs))
	}
	// 回调在**实际调用它的位置**（Run 内部的 f()）展开
	callbacks := seqCallNodesNamed(runs[0], "f")
	if len(callbacks) != 1 {
		t.Fatalf("Run 内部应有 1 个回调调用节点 f，got %d", len(callbacks))
	}
	cb := callbacks[0]
	if cb.Evidence == nil || cb.Evidence.Source != SeqEvidenceClosure ||
		!strings.Contains(cb.Evidence.Reason, "callback-binding") {
		t.Errorf("回调节点证据应来自 closure 且 reason 含 callback-binding，got %+v", cb.Evidence)
	}
	if got := seqCallNodesNamed(cb, "Delivered"); len(got) != 1 {
		t.Errorf("回调内部调用应在回调节点下展开一次 Delivered，got %d", len(got))
	}
}

// TestSeqFuncValueUnbound：有明确调用点但回调值无法确定 → unknown 节点。
func TestSeqFuncValueUnbound(t *testing.T) {
	src := `package m

func Run(f func()) { f() }

func Entry(f func()) { Run(f) }
`
	const pkg = "symbol:go:example.com/m:"
	acts, dir := seedFuncValueRepo(t, src, map[string]string{"Run": pkg + "Run"})
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Entry", RepoAbs: dir, Depth: 4})
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}
	runs := seqCallNodesNamed(root, "Run")
	if len(runs) != 1 {
		t.Fatalf("应有 1 个 Run 节点，got %d", len(runs))
	}
	callbacks := seqCallNodesNamed(runs[0], "f")
	if len(callbacks) != 1 {
		t.Fatalf("Run 内部应保留 1 个 f 调用节点（unknown），got %d", len(callbacks))
	}
	cb := callbacks[0]
	if cb.Stop == nil || cb.Stop.Code != SeqStopFuncValueUnbound {
		t.Errorf("未绑定的回调应带 %s 诊断，got %+v", SeqStopFuncValueUnbound, cb.Stop)
	}
	if cb.Evidence == nil || !cb.Evidence.Ambiguous {
		t.Errorf("未绑定的回调应标 ambiguous（unverified），got %+v", cb.Evidence)
	}
	if len(cb.Nodes) != 0 {
		t.Errorf("未绑定的回调不得展开内部执行，got %d 子节点", len(cb.Nodes))
	}
}

// TestSeqFuncValueEscape：函数值在未解析/外部调用里逃逸 → 标注原因。
func TestSeqFuncValueEscape(t *testing.T) {
	src := `package m

func Entry(f func()) { External(f) }
`
	acts, dir := seedFuncValueRepo(t, src, map[string]string{})
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Entry", RepoAbs: dir, Depth: 3})
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}
	nodes := seqCallNodesNamed(root, "External")
	if len(nodes) != 1 {
		t.Fatalf("应有 1 个 External 调用节点，got %d", len(nodes))
	}
	if nodes[0].Stop == nil || !strings.Contains(nodes[0].Stop.Detail, "逃逸") {
		t.Errorf("逃逸边界应标注原因，got %+v", nodes[0].Stop)
	}
}

// TestSeqFuncValueNamedVar：局部变量指向包级函数 → 调用处展开该函数。
func TestSeqFuncValueNamedVar(t *testing.T) {
	src := `package m

func Body() {}

func Named() { Body() }

func Entry() { f := Named; f() }
`
	const pkg = "symbol:go:example.com/m:"
	calls := map[string]string{"Body": pkg + "Body"}
	acts, dir := seedFuncValueRepo(t, src, calls)
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Entry", RepoAbs: dir, Depth: 4})
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}
	callsF := seqCallNodesNamed(root, "f")
	if len(callsF) != 1 {
		t.Fatalf("应有 1 个 f() 调用节点，got %d", len(callsF))
	}
	if callsF[0].Evidence == nil || callsF[0].Evidence.SymbolID != pkg+"Named" {
		t.Errorf("f() 应展开到 Named，证据 %+v", callsF[0].Evidence)
	}
	if got := seqCallNodesNamed(callsF[0], "Body"); len(got) != 1 {
		t.Errorf("Named 的函数体应在 f() 下展开（Body 一次），got %d", len(got))
	}
}
