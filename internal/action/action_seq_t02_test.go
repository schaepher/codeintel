package action

// T02：函数列表路径——元素顺序/item 渲染、list... 转发、空列表、append、
// 遮蔽 append、未知列表不视为空。

import (
	"strings"
	"testing"
)

const t02Pkg = "symbol:go:example.com/m:"

func t02Seed(t *testing.T, src string) (*Actions, string) {
	t.Helper()
	return seedFuncValueRepo(t, src, map[string]string{
		"Run": t02Pkg + "Run", "Forward": t02Pkg + "Forward",
	})
}

// t02ListBranch 找 callback list 分支并按顺序返回 item 标签。
func t02ListBranch(t *testing.T, root *CodeSeqNode) (*CodeSeqNode, []string) {
	t.Helper()
	branches := seqFindNodes(root, func(n *CodeSeqNode) bool {
		return n.Kind == "branch" && n.Label == "callback list path"
	})
	if len(branches) != 1 {
		t.Fatalf("应有 1 个 callback list path 分支，got %d: %v", len(branches), labelsOf(root.Nodes))
	}
	b := branches[0]
	if b.Scene == nil || b.Scene.Status != SceneTaken || b.Scene.Reason != "source-callback-list" {
		t.Errorf("列表分支应为 taken/source-callback-list: %+v", b.Scene)
	}
	var items []string
	for _, n := range b.Nodes {
		if n.Kind == "loop" {
			items = append(items, n.Label)
		}
	}
	return b, items
}

func TestT02CallbackListOrderAndVerdict(t *testing.T) {
	src := `package m

type Fn func()

func A()    { HitA() }
func B()    { HitB() }
func HitA() {}
func HitB() {}

func Run(fs ...Fn) {
	for _, f := range fs {
		f()
	}
}

func Entry() { Run(A, B) }
`
	acts, dir := t02Seed(t, src)
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Entry", RepoAbs: dir, Depth: 6})
	if err != nil {
		t.Fatal(err)
	}
	b, items := t02ListBranch(t, root)
	if strings.Join(items, ",") != "item 1/2,item 2/2" {
		t.Fatalf("item 顺序 = %v", items)
	}
	// 每个 item 内先调用元素函数再展开其内部（A→HitA、B→HitB）。
	for i, wantFn := range []string{"A", "B"} {
		item := b.Nodes[i]
		var fnCalls []string
		for _, n := range item.Nodes {
			if n.Kind == "call" {
				fnCalls = append(fnCalls, n.Label)
			}
		}
		if len(fnCalls) == 0 || fnCalls[0] != "f" {
			t.Errorf("item %d 首个调用应为 f（元素值），got %v", i+1, fnCalls)
		}
		// 元素的 canonical 身份来自列表顺序
		ev := item.Nodes[0].Evidence
		if ev == nil || ev.SymbolID != t02Pkg+wantFn {
			t.Errorf("item %d 元素身份 = %+v, want %s", i+1, ev, wantFn)
		}
	}
	for _, lbl := range []string{"HitA", "HitB"} {
		calls := seqCallNodesNamed(root, lbl)
		if len(calls) != 1 {
			t.Errorf("%s 应执行一次，got %d", lbl, len(calls))
			continue
		}
		if calls[0].Evidence == nil || calls[0].Evidence.Verdict != SeqVerdictCorroborated {
			t.Errorf("%s 在回调路径上应 corroborated: %+v", lbl, calls[0].Evidence)
		}
	}
}

// list... 转发：命名变参 → Forward(fs...) → range（转发后元素顺序不变）。
func TestT02ListForwarding(t *testing.T) {
	src := `package m

type Fn func()

func A()    { Hit() }
func Hit()  {}

func Forward(fs ...Fn) {
	for _, f := range fs {
		f()
	}
}

func Run(fs ...Fn) { Forward(fs...) }

func Entry() { Run(A) }
`
	acts, dir := t02Seed(t, src)
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Entry", RepoAbs: dir, Depth: 8})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(seqCallNodesNamed(root, "Hit")); got != 1 {
		t.Fatalf("转发后应执行一次 Hit，got %d: %v", got, labelsOf(root.Nodes))
	}
}

// 无实际变参 → 确定空列表；转发后仍为空（零元素执行，不污染原回调）。
func TestT02EmptyListForwarded(t *testing.T) {
	src := `package m

type Fn func()

func Hit() {}

func Forward(fs ...Fn) {
	for _, f := range fs {
		f()
	}
}

func Run(fs ...Fn) { Forward(fs...) }

func Entry() { Run() }
`
	acts, dir := t02Seed(t, src)
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Entry", RepoAbs: dir, Depth: 8})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(seqCallNodesNamed(root, "Hit")); got != 0 {
		t.Fatalf("空列表零元素执行，got %d Hit", got)
	}
	if got := len(seqFindNodes(root, func(n *CodeSeqNode) bool {
		return n.Kind == "branch" && n.Label == "callback list path"
	})); got != 0 {
		t.Errorf("空列表不应生成元素分支，got %d", got)
	}
}

// 本地 append：元素按追加顺序执行。
func TestT02AppendList(t *testing.T) {
	src := `package m

type Fn func()

func A()    { HitA() }
func B()    { HitB() }
func HitA() {}
func HitB() {}

func Run(fs ...Fn) {
	for _, f := range fs {
		f()
	}
}

func Entry() {
	list := []Fn{A}
	list = append(list, B)
	Run(list...)
}
`
	acts, dir := t02Seed(t, src)
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Entry", RepoAbs: dir, Depth: 6})
	if err != nil {
		t.Fatal(err)
	}
	_, items := t02ListBranch(t, root)
	if len(items) != 2 {
		t.Fatalf("append 后应有 2 个元素，got %v", items)
	}
	if len(seqCallNodesNamed(root, "HitA")) != 1 || len(seqCallNodesNamed(root, "HitB")) != 1 {
		t.Errorf("append 元素应各执行一次: %v", labelsOf(root.Nodes))
	}
}

// 未知列表（形参未绑定）：不视为空，也不猜顺序。
func TestT02UnknownListNotTreatedAsEmpty(t *testing.T) {
	src := `package m

type Fn func()

func Hit() {}

func Run(fs ...Fn) {
	for _, f := range fs {
		f()
	}
}

func Entry(fs []Fn) { Run(fs...) }
`
	acts, dir := t02Seed(t, src)
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Entry", RepoAbs: dir, Depth: 6})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(seqCallNodesNamed(root, "Hit")); got != 0 {
		t.Fatalf("未知列表不得展开确定执行（got %d Hit）", got)
	}
	// 必须留下"不确定"的诊断（不能静默当成空列表）
	loops := seqFindNodes(root, func(n *CodeSeqNode) bool {
		return n.Kind == "loop" && n.Stop != nil
	})
	if len(loops) == 0 {
		t.Errorf("未知列表应保留停止/未知诊断: %v", labelsOf(root.Nodes))
	}
}
