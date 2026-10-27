package action

// todo22 §2.1 测试：统一捕获入口（captureSeqClosureValue / ClosureArgs /
// CallbackLists）+ 绑定元数据（声明对象/条件）+ 方法值。
//
// 覆盖"没有覆盖 ≠ 明确未知"、同名阴影不借用旧绑定、方法值按 receiver 的
// 类型数据流（对象身份）解析。

import (
	"strings"
	"testing"
)

// TestSeqCaptureMethodValue：`f := s.Run` / `Use(s.Run)` 按 receiver 类型
// 解析成 (S).Run，并在调用处展开。
func TestSeqCaptureMethodValue(t *testing.T) {
	src := `package m

type S struct{}

func (s *S) Run() { Target() }

func Target() {}

func Use(fn func()) { fn() }

func Entry() {
	s := &S{}
	f := s.Run
	f()
	Use(s.Run)
}
`
	const pkg = "symbol:go:example.com/m:"
	acts, dir := seedFuncValueRepo(t, src, map[string]string{"Use": pkg + "Use"})
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Entry", RepoAbs: dir, Depth: 5})
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}
	// f() 与 Use 的回调 fn() 都应落到 (S).Run
	runID := pkg + "(S).Run"
	named := seqFindNodes(root, func(n *CodeSeqNode) bool {
		return n.Kind == "call" && n.Evidence != nil && n.Evidence.SymbolID == runID
	})
	if len(named) < 2 {
		t.Fatalf("方法值应解析到 %s（f() 与 Use 回调各一次），got %d\n%+v", runID, len(named), named)
	}
	// 每个展开点都要展开 Run 的函数体（Target 一次）
	if got := seqCallNodesNamed(root, "Target"); len(got) < 2 {
		t.Errorf("方法值展开后应执行 Target（>=2 次），got %d", len(got))
	}
}

// TestSeqCaptureUnknownVsUnbound：没有覆盖 vs 明确未知 vs 阴影。
func TestSeqCaptureUnknownVsUnbound(t *testing.T) {
	src := `package m

func A() {}
func B() {}

func Mutate() int { return 1 }

func NoBinding(f func()) { f() }

func Overwritten() {
	f := func() { A() }
	f = Mutate()
	f()
}

func Shadowed() {
	f := func() { A() }
	{
		f := func() { B() }
		f()
	}
	f()
}
`
	acts, dir := seedFuncValueRepo(t, src, map[string]string{})

	// ① 没有覆盖（形参无绑定）→ unknown、无子节点
	no, err := acts.CodeSequence(CodeSequenceRequest{Target: "NoBinding", RepoAbs: dir, Depth: 4})
	if err != nil {
		t.Fatal(err)
	}
	fNode := seqCallNodesNamed(no, "f")
	if len(fNode) != 1 {
		t.Fatalf("NoBinding 应有 1 个 f()，got %d", len(fNode))
	}
	if fNode[0].Stop == nil || fNode[0].Stop.Code != SeqStopFuncValueUnbound {
		t.Errorf("无绑定形参应是 func_value_unbound，got %+v", fNode[0].Stop)
	}

	// ② 明确未知（被未知调用改写）→ 不能回退到旧字面量（A 不执行）
	ow, err := acts.CodeSequence(CodeSequenceRequest{Target: "Overwritten", RepoAbs: dir, Depth: 4})
	if err != nil {
		t.Fatal(err)
	}
	if got := seqCallNodesNamed(ow, "A"); len(got) != 0 {
		t.Errorf("被覆盖后的未知值不得回退到旧字面量（A 不应执行），got %d", len(got))
	}
	unknownCall := seqCallNodesNamed(ow, "f")
	if len(unknownCall) != 1 || unknownCall[0].Stop == nil ||
		unknownCall[0].Stop.Code != SeqStopFuncValueUnbound {
		t.Errorf("覆盖为未知值后 f() 应是明确 unknown，got %+v", unknownCall)
	}

	// ③ 阴影：内层执行 B、外层执行 A（按 AST 对象身份，不串线）
	sh, err := acts.CodeSequence(CodeSequenceRequest{Target: "Shadowed", RepoAbs: dir, Depth: 4})
	if err != nil {
		t.Fatal(err)
	}
	if got := seqCallNodesNamed(sh, "A"); len(got) != 1 {
		t.Errorf("外层 f() 应只执行 A 一次，got %d", len(got))
	}
	if got := seqCallNodesNamed(sh, "B"); len(got) != 1 {
		t.Errorf("内层阴影 f() 应只执行 B 一次，got %d", len(got))
	}
}

// TestSeqCaptureConditionalBindings：条件覆盖的候选带条件文本（可核对依据）。
func TestSeqCaptureConditionalBindings(t *testing.T) {
	src := `package m

func A() {}
func B() {}

func Entry(cond bool) {
	var f func()
	if cond {
		f = func() { A() }
	} else {
		f = func() { B() }
	}
	f()
}
`
	acts, dir := seedFuncValueRepo(t, src, map[string]string{})
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Entry", RepoAbs: dir, Depth: 4})
	if err != nil {
		t.Fatal(err)
	}
	calls := seqCallNodesNamed(root, "f")
	if len(calls) != 1 {
		t.Fatalf("应有 1 个 f()，got %d", len(calls))
	}
	ev := calls[0].Evidence
	if ev == nil || !ev.Ambiguous || len(ev.Candidates) != 2 {
		t.Fatalf("条件覆盖应保留 2 个未消歧候选，got %+v", ev)
	}
	joined := strings.Join(ev.Candidates, " | ")
	if !strings.Contains(joined, "when cond") || !strings.Contains(joined, "when !(cond)") {
		t.Errorf("候选应带各自的条件依据，got %q", joined)
	}
	if got := seqCallNodesNamed(calls[0], "A"); len(got) != 1 {
		t.Errorf("then 候选应展开 A 一次，got %d", len(got))
	}
	if got := seqCallNodesNamed(calls[0], "B"); len(got) != 1 {
		t.Errorf("else 候选应展开 B 一次，got %d", len(got))
	}
}

// TestSeqCaptureRecursiveFactory：自引用工厂不能无限展开（深度上限 → unknown）。
func TestSeqCaptureRecursiveFactory(t *testing.T) {
	src := `package m

func F() func() { return F() }

func Entry() { g := F(); g() }
`
	acts, dir := seedFuncValueRepo(t, src, map[string]string{})
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Entry", RepoAbs: dir, Depth: 5})
	if err != nil {
		t.Fatalf("CodeSequence 不应因自引用工厂挂死/报错: %v", err)
	}
	calls := seqCallNodesNamed(root, "g")
	if len(calls) != 1 {
		t.Fatalf("应有 1 个 g()，got %d", len(calls))
	}
	// 必须明确未知（而不是猜一个返回值，也不是无限递归）
	if calls[0].Stop == nil && len(calls[0].Nodes) == 0 {
		t.Logf("g() 输出: %+v", calls[0].Evidence)
	}
	found := false
	walkSeqAll(root, func(n *CodeSeqNode) {
		if n.Stop != nil && (n.Stop.Code == SeqStopFuncValueUnbound || strings.Contains(n.Stop.Detail, "深度")) {
			found = true
		}
	})
	if !found {
		t.Errorf("自引用工厂应给出明确 unknown/深度诊断")
	}
}
