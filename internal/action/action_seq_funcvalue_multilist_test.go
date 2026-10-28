package action

// todo25 §2.4：条件分支内 append/覆盖的反例矩阵。

import (
	"strings"
	"testing"
)

// seqNodesWithAmbigAncestor 收集满足 pred 且**祖先链**含 ambiguous 分支节点的
// 节点（条件尾部元素的执行证明必须带歧义）。
func seqNodesWithAmbigAncestor(root *CodeSeqNode, pred func(*CodeSeqNode) bool) []*CodeSeqNode {
	var out []*CodeSeqNode
	var walk func(n *CodeSeqNode, amb bool)
	walk = func(n *CodeSeqNode, amb bool) {
		if n == nil {
			return
		}
		if amb && pred(n) {
			out = append(out, n)
		}
		next := amb || (n.Evidence != nil && n.Evidence.Ambiguous)
		for _, c := range n.Nodes {
			walk(c, next)
		}
		for _, c := range n.Else {
			walk(c, next)
		}
	}
	walk(root, false)
	return out
}

func seqHitNodes(root *CodeSeqNode, label string) []*CodeSeqNode {
	return seqFindNodes(root, func(n *CodeSeqNode) bool {
		return n.Kind == "call" && n.Label == label
	})
}

// TestSeqFuncValueConditionalAppend：`if cond { append(C) } else { append(D) }`
// 的列表——公共前缀 A/B 确定执行；C/D 分别出现在互斥候选分支（带歧义），
// 不因为分支覆盖把整段列表退化成"不展开"。
func TestSeqFuncValueConditionalAppend(t *testing.T) {
	src := `package m

func HitA() {}
func HitB() {}
func HitC() {}
func HitD() {}
func A() { HitA() }
func B() { HitB() }
func C() { HitC() }
func D() { HitD() }

func UseList(list []func()) {
	for _, f := range list {
		f()
	}
}

func Entry(cond bool) {
	list := []func(){A, B}
	if cond {
		list = append(list, C)
	} else {
		list = append(list, D)
	}
	UseList(list)
}
`
	acts, dir := seedFuncValueRepo(t, src, map[string]string{})
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Entry", RepoAbs: dir, Depth: 6})
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}
	use := seqCallNodesNamed(root, "UseList")
	if len(use) != 1 {
		t.Fatalf("应有 1 个 UseList 调用，got %d", len(use))
	}
	for _, lab := range []string{"HitA", "HitB"} {
		if got := seqHitNodes(use[0], lab); len(got) != 1 {
			t.Errorf("公共前缀元素应确定执行一次（%s=%d）", lab, len(got))
		}
	}
	for _, lab := range []string{"HitC", "HitD"} {
		amb := seqNodesWithAmbigAncestor(use[0], func(n *CodeSeqNode) bool {
			return n.Kind == "call" && n.Label == lab
		})
		if len(amb) != 1 {
			t.Errorf("条件 append 元素应在一条歧义分支内执行（%s=%d）", lab, len(amb))
		}
		if got := seqHitNodes(use[0], lab); len(got) != 1 {
			t.Errorf("条件 append 元素不得重复展开（%s=%d）", lab, len(got))
		}
	}
	loops := seqFindNodes(use[0], func(n *CodeSeqNode) bool { return n.Kind == "loop" })
	if len(loops) != 1 {
		t.Fatalf("应有 1 个 range 循环节点，got %d", len(loops))
	}
	if len(loops[0].Nodes) != 4 {
		t.Errorf("range 节点应保留公共前缀 2 项 + 两个互斥候选分支，got %d 子节点", len(loops[0].Nodes))
	}
}

// TestSeqFuncValueConditionalOverwrite：分支覆盖成互斥列表（无公共前缀）时
// 两个候选各自展开，且都带歧义；不拼成"先 A 后 B"的顺序。
func TestSeqFuncValueConditionalOverwrite(t *testing.T) {
	src := `package m

func HitA() {}
func HitB() {}
func A() { HitA() }
func B() { HitB() }

func UseList(list []func()) {
	for _, f := range list {
		f()
	}
}

func Entry(cond bool) {
	list := []func(){A}
	if cond {
		list = []func(){B}
	}
	UseList(list)
}
`
	acts, dir := seedFuncValueRepo(t, src, map[string]string{})
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Entry", RepoAbs: dir, Depth: 6})
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}
	use := seqCallNodesNamed(root, "UseList")
	if len(use) != 1 {
		t.Fatalf("应有 1 个 UseList 调用，got %d", len(use))
	}
	for _, lab := range []string{"HitA", "HitB"} {
		amb := seqNodesWithAmbigAncestor(use[0], func(n *CodeSeqNode) bool {
			return n.Kind == "call" && n.Label == lab
		})
		if len(amb) != 1 {
			t.Errorf("互斥列表候选 %s 应在一条歧义分支内执行一次，got %d", lab, len(amb))
		}
	}
}

// TestSeqFuncValueConditionalListBudget：多候选列表的公共前缀超过迭代预算
// → 明确诊断（不截取前 16 项冒充完整）。
func TestSeqFuncValueConditionalListBudget(t *testing.T) {
	var b strings.Builder
	b.WriteString("package m\n\nfunc A() {}\nfunc C() {}\nfunc D() {}\n")
	b.WriteString("func UseList(list []func()) {\n\tfor _, f := range list {\n\t\tf()\n\t}\n}\n\n")
	b.WriteString("func Entry(cond bool) {\n\tlist := []func(){")
	for i := 0; i <= seqMaxRangeIters; i++ {
		b.WriteString("A,")
	}
	b.WriteString("}\n\tif cond {\n\t\tlist = append(list, C)\n\t} else {\n\t\tlist = append(list, D)\n\t}\n\tUseList(list)\n}\n")
	acts, dir := seedFuncValueRepo(t, b.String(), map[string]string{})
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Entry", RepoAbs: dir, Depth: 6})
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}
	found := false
	walkSeqAll(root, func(n *CodeSeqNode) {
		if n.Kind == "loop" && n.Stop != nil && strings.Contains(n.Stop.Detail, "预算") {
			found = true
		}
	})
	if !found {
		t.Errorf("公共前缀超过迭代预算应给出明确诊断")
	}
}
