package action

// todo22 §2.3/§2.4 测试：工厂返回函数值（具名/字面量/条件返回）、函数列表
// （字面量/append/变参/range 折叠）、空列表与预算。

import (
	"strings"
	"testing"
)

// seqFlatLabels 先序展开树里的 label 序列（用于断言执行顺序）。
func seqFlatLabels(n *CodeSeqNode, out *[]string) {
	if n == nil {
		return
	}
	if n.Kind == "call" && n.Label != "" {
		*out = append(*out, n.Label)
	}
	for _, c := range n.Nodes {
		seqFlatLabels(c, out)
	}
	for _, c := range n.Else {
		seqFlatLabels(c, out)
	}
}

func TestSeqFuncValueFactoryReturns(t *testing.T) {
	src := `package m

func A() {}
func B() {}
func Named() {}

func MakeNamed() func() { return Named }

func MakeLit(wrap func()) func() {
	return func() { wrap() }
}

func MakeCond(flag bool, f func()) func() {
	if flag {
		return f
	}
	return func() { A() }
}

func Entry() {
	f := MakeNamed()
	f()
	g := MakeLit(func() { A() })
	g()
	h := MakeCond(true, func() { B() })
	h()
}
`
	acts, dir := seedFuncValueRepo(t, src, map[string]string{})
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Entry", RepoAbs: dir, Depth: 5})
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}

	// ① 工厂返回具名函数：f() 展开 Named（Named 体无调用 → no_calls）
	callsF := seqCallNodesNamed(root, "f")
	if len(callsF) != 1 {
		t.Fatalf("应有 1 个 f()，got %d", len(callsF))
	}
	if ev := callsF[0].Evidence; ev == nil || ev.SymbolID != "symbol:go:example.com/m:Named" {
		t.Errorf("f() 应展开到工厂返回的 Named，证据 %+v", ev)
	}
	// ② 工厂返回字面量：g() 展开字面量体 → A 一次
	callsG := seqCallNodesNamed(root, "g")
	if len(callsG) != 1 {
		t.Fatalf("应有 1 个 g()，got %d", len(callsG))
	}
	if got := seqCallNodesNamed(callsG[0], "A"); len(got) != 1 {
		t.Errorf("g() 应展开工厂返回闭包里的 A（1 次），got %d", len(got))
	}
	// ③ 条件返回：两条 return 路径都保留 → 未消歧多候选，A/B 分别展开
	callsH := seqCallNodesNamed(root, "h")
	if len(callsH) != 1 {
		t.Fatalf("应有 1 个 h()，got %d", len(callsH))
	}
	if ev := callsH[0].Evidence; ev == nil || !ev.Ambiguous || len(ev.Candidates) < 2 {
		t.Errorf("条件返回应保留未消歧多候选（>=2），证据 %+v", ev)
	}
	if got := seqCallNodesNamed(callsH[0], "A"); len(got) == 0 {
		t.Errorf("条件返回候选（字面量）应展开 A")
	}
	if got := seqCallNodesNamed(callsH[0], "B"); len(got) != 1 {
		t.Errorf("条件返回候选（传入回调）应展开 B 一次，got %d", len(got))
	}
}

func TestSeqFuncValueListsAndFold(t *testing.T) {
	src := `package m

type Dec func(func()) func()

func A() {}
func B() {}
func Before1() {}
func Before2() {}

func W1(f func()) func() { return func() { Before1(); f() } }
func W2(f func()) func() { return func() { Before2(); f() } }

func UseList(list []func()) {
	for _, f := range list {
		f()
	}
}

func EmptyLoop() {
	var empty []func()
	for _, f := range empty {
		f()
	}
}

func Fold() {
	process := func() { A() }
	decs := []Dec{W1, W2}
	for _, d := range decs {
		process = d(process)
	}
	process()
}

func Entry() {
	UseList([]func(){A, B})
	list := []func(){A}
	list = append(list, B)
	UseList(list)
	EmptyLoop()
}
`
	acts, dir := seedFuncValueRepo(t, src, map[string]string{})
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Entry", RepoAbs: dir, Depth: 6})
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}

	// ① 列表字面量：range 逐项执行（A、B 各一次）
	use := seqCallNodesNamed(root, "UseList")
	if len(use) != 2 {
		t.Fatalf("应有 2 个 UseList 调用，got %d", len(use))
	}
	first := append([]string{}, "")
	_ = first
	const pkg = "symbol:go:example.com/m:"
	gotA := seqFindNodes(use[0], func(n *CodeSeqNode) bool {
		return n.Kind == "call" && n.Evidence != nil && n.Evidence.SymbolID == pkg+"A"
	})
	gotB := seqFindNodes(use[0], func(n *CodeSeqNode) bool {
		return n.Kind == "call" && n.Evidence != nil && n.Evidence.SymbolID == pkg+"B"
	})
	if len(gotA) != 1 || len(gotB) != 1 {
		t.Errorf("列表字面量 range 应执行 A 与 B 各一次（A=%d B=%d）", len(gotA), len(gotB))
	}
	// ② append 扩展后的列表：第二条 UseList 也是 A、B 各一次
	gotA2 := seqFindNodes(use[1], func(n *CodeSeqNode) bool {
		return n.Kind == "call" && n.Evidence != nil && n.Evidence.SymbolID == pkg+"A"
	})
	gotB2 := seqFindNodes(use[1], func(n *CodeSeqNode) bool {
		return n.Kind == "call" && n.Evidence != nil && n.Evidence.SymbolID == pkg+"B"
	})
	if len(gotA2) != 1 || len(gotB2) != 1 {
		t.Errorf("append 扩展后的列表应执行 A 与 B 各一次（A=%d B=%d）", len(gotA2), len(gotB2))
	}

	// ③ 空列表：range 不执行（不污染、不产出调用）
	empty := seqFindNodes(root, func(n *CodeSeqNode) bool {
		return n.Kind == "loop" && strings.Contains(n.Label, "empty")
	})
	if len(empty) != 1 {
		t.Fatalf("应有 1 个遍历 empty 的循环，got %d", len(empty))
	}
	if len(empty[0].Nodes) != 0 {
		t.Errorf("空列表循环不得执行循环体，got %d 子节点", len(empty[0].Nodes))
	}

	// ④ 两层装饰器折叠：process() → Before2 → Before1 → A（顺序即折叠顺序）
	foldRoot, err := acts.CodeSequence(CodeSequenceRequest{Target: "Fold", RepoAbs: dir, Depth: 6})
	if err != nil {
		t.Fatalf("CodeSequence(Fold): %v", err)
	}
	var labels []string
	seqFlatLabels(foldRoot, &labels)
	idxBefore2, idxBefore1, idxA := -1, -1, -1
	for i, l := range labels {
		switch l {
		case "Before2":
			if idxBefore2 < 0 {
				idxBefore2 = i
			}
		case "Before1":
			if idxBefore1 < 0 {
				idxBefore1 = i
			}
		case "A":
			if idxA < 0 {
				idxA = i
			}
		}
	}
	if idxBefore2 < 0 || idxBefore1 < 0 || idxA < 0 || !(idxBefore2 < idxBefore1 && idxBefore1 < idxA) {
		t.Errorf("两层装饰器应按迭代顺序折叠（Before2 < Before1 < A），got %v", labels)
	}
}

// TestSeqFuncValueListBudget：列表元素 >32 → 明确 unknown；range 迭代 >16
// → 明确诊断，不截取前几项冒充完整。
func TestSeqFuncValueListBudget(t *testing.T) {
	var lit strings.Builder
	lit.WriteString("package m\n\nfunc A() {}\n\nfunc UseList(list []func()) {\n\tfor _, f := range list {\n\t\tf()\n\t}\n}\n\nfunc Entry() {\n\tlist := []func(){")
	for i := 0; i < seqMaxListItems+1; i++ {
		lit.WriteString("A,")
	}
	lit.WriteString("}\n\tUseList(list)\n}\n")
	acts, dir := seedFuncValueRepo(t, lit.String(), map[string]string{})
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Entry", RepoAbs: dir, Depth: 4})
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}
	found := false
	walkSeqAll(root, func(n *CodeSeqNode) {
		if n.Stop != nil && strings.Contains(n.Stop.Detail, "预算") {
			found = true
		}
	})
	if !found {
		t.Errorf("超过元素预算应给出明确诊断（不静默截断）")
	}
}

// walkSeqAll 遍历全部节点（含 Else）。
func walkSeqAll(n *CodeSeqNode, fn func(*CodeSeqNode)) {
	if n == nil {
		return
	}
	fn(n)
	for _, c := range n.Nodes {
		walkSeqAll(c, fn)
	}
	for _, c := range n.Else {
		walkSeqAll(c, fn)
	}
}
