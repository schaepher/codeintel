package action

// todo22 §2（第一增量）测试：字面量/IIFE/局部函数值/条件多候选/求值顺序。

import "testing"

// TestSeqFuncValueLiterals：字面量/IIFE/覆盖顺序/条件多候选/求值顺序。
func TestSeqFuncValueLiterals(t *testing.T) {
	src := `package m

func NeverCalled() {}
func Called() {}
func Overwritten() {}
func Latest() {}
func CondA() {}
func CondB() {}
func Iife() {}
func Factory() {}
func ArgLit() {}

func Use(a func(), b func()) {}

func Entry(cond bool) {
	unused := func() { NeverCalled() }
	_ = unused
	f := func() { Called() }
	f()
	g := func() { Overwritten() }
	g = func() { Latest() }
	g()
	if cond {
		g = func() { CondA() }
	} else {
		g = func() { CondB() }
	}
	g()
	func() { Iife() }()
	Use(Factory(), func() { ArgLit() })
}
`
	const pkg = "symbol:go:example.com/m:"
	calls := map[string]string{
		"NeverCalled": pkg + "NeverCalled", "Called": pkg + "Called",
		"Overwritten": pkg + "Overwritten", "Latest": pkg + "Latest",
		"CondA": pkg + "CondA", "CondB": pkg + "CondB", "Iife": pkg + "Iife",
		"Factory": pkg + "Factory", "ArgLit": pkg + "ArgLit", "Use": pkg + "Use",
	}
	acts, dir := seedFuncValueRepo(t, src, calls)
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Entry", RepoAbs: dir, Depth: 4})
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}

	// ① 未调用的局部字面量、作为实参传入但未被调用的回调：不执行
	if got := seqCallNodesNamed(root, "NeverCalled"); len(got) != 0 {
		t.Errorf("未调用的局部字面量不得执行内部调用，got %d 个 NeverCalled", len(got))
	}
	if got := seqCallNodesNamed(root, "ArgLit"); len(got) != 0 {
		t.Errorf("作为实参传入但未调用的回调不得执行内部调用，got %d 个 ArgLit", len(got))
	}
	// ② 调用点执行一次（不是字面量创建处）
	if got := seqCallNodesNamed(root, "Called"); len(got) != 1 {
		t.Errorf("f() 应执行一次 Called，got %d", len(got))
	}
	// ③ 调用前被覆盖：只执行最新值
	if got := seqCallNodesNamed(root, "Overwritten"); len(got) != 0 {
		t.Errorf("调用前被覆盖的旧字面量不得执行，got %d 个 Overwritten", len(got))
	}
	if got := seqCallNodesNamed(root, "Latest"); len(got) != 1 {
		t.Errorf("覆盖后调用应执行 Latest 一次，got %d", len(got))
	}
	// ④ 条件分支：两个候选都保留、分别展开，且整体标未消歧
	if len(seqCallNodesNamed(root, "CondA")) == 0 || len(seqCallNodesNamed(root, "CondB")) == 0 {
		t.Errorf("条件覆盖的两个候选都应分别展开：CondA=%d CondB=%d",
			len(seqCallNodesNamed(root, "CondA")), len(seqCallNodesNamed(root, "CondB")))
	}
	ambiguous := seqFindNodes(root, func(n *CodeSeqNode) bool {
		return n.Kind == "call" && n.Evidence != nil && n.Evidence.Ambiguous &&
			n.Evidence.Source == SeqEvidenceClosure
	})
	if len(ambiguous) == 0 {
		t.Errorf("条件覆盖后的调用点应标 ambiguous（不得把某个候选当唯一结论）")
	}
	// ⑤ IIFE 执行一次
	if got := seqCallNodesNamed(root, "Iife"); len(got) != 1 {
		t.Errorf("IIFE 应执行一次 Iife，got %d", len(got))
	}
	// ⑥ 实参工厂按求值顺序先于外层调用
	var factoryIdx, useIdx = -1, -1
	for i, n := range root.Nodes {
		if n.Kind == "call" && n.Label == "Factory" && factoryIdx < 0 {
			factoryIdx = i
		}
		if n.Kind == "call" && n.Label == "Use" && useIdx < 0 {
			useIdx = i
		}
	}
	if factoryIdx < 0 || useIdx < 0 || factoryIdx > useIdx {
		t.Errorf("实参工厂应先于外层调用（Factory idx=%d, Use idx=%d）", factoryIdx, useIdx)
	}
}
