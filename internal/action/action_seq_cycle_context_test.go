package action

// todo23 §2.5：防环身份包含 repo/符号/入站回调的词法声明与绑定站点——
// 同一 executor 经不同回调上下文可继续展开；同一上下文递归只截断一次。

import (
	"testing"
)

const cycleCtxSrc = `package m

func Entry() { Execute(func() { Middle() }) }

func Execute(f func()) { f() }

func Middle() { Execute(func() { Sink() }) }

func Sink() {}

func EntryLoop() { Again(func() { Sink() }) }

func Again(f func()) { Again(f) }
`

func seedCycleCtxRepo(t *testing.T) (*Actions, string) {
	t.Helper()
	pkg := "symbol:go:example.com/m:"
	calls := map[string]string{
		"Execute": pkg + "Execute",
		"Middle":  pkg + "Middle",
		"Sink":    pkg + "Sink",
		"Again":   pkg + "Again",
	}
	return seedDIRepo(t, cycleCtxSrc, calls)
}

func countStopCode(root *CodeSeqNode, code string) int {
	n := 0
	var walk func(nodes []*CodeSeqNode)
	walk = func(nodes []*CodeSeqNode) {
		for _, x := range nodes {
			if x.Stop != nil && x.Stop.Code == code {
				n++
			}
			walk(x.Nodes)
			walk(x.Else)
		}
	}
	if root != nil {
		walk(root.Nodes)
	}
	return n
}

// TestCycleCallbackContextDistinct：两个不同回调上下文都展开，Sink 恰好
// 一次，无 cycle 诊断。
func TestCycleCallbackContextDistinct(t *testing.T) {
	acts, dir := seedCycleCtxRepo(t)
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/m:Entry", RepoAbs: dir, Depth: 16})
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}
	if got := len(seqCallNodesNamed(root, "Execute")); got != 2 {
		t.Errorf("两个不同回调上下文都应展开 Execute，got %d", got)
	}
	if got := len(seqCallNodesNamed(root, "Sink")); got != 1 {
		t.Errorf("Sink 应恰好一次，got %d", got)
	}
	if n := countStopCode(root, SeqStopCycle); n != 0 {
		t.Errorf("不同回调上下文不应判循环，got %d 个 cycle", n)
	}
}

// TestCycleSameCallbackContextTruncated：同一回调上下文递归 → 恰好一个
// cycle 截断，Sink 为零；不同 depth 都按同一身份及时防环。
func TestCycleSameCallbackContextTruncated(t *testing.T) {
	acts, dir := seedCycleCtxRepo(t)
	for _, depth := range []int{16, 24, 48} {
		root, err := acts.CodeSequence(CodeSequenceRequest{
			Target: "symbol:go:example.com/m:EntryLoop", RepoAbs: dir, Depth: depth})
		if err != nil {
			t.Fatalf("depth=%d CodeSequence: %v", depth, err)
		}
		if got := countStopCode(root, SeqStopCycle); got != 1 {
			t.Errorf("depth=%d 应有 1 个 cycle 截断，got %d", depth, got)
		}
		if got := len(seqCallNodesNamed(root, "Sink")); got != 0 {
			t.Errorf("depth=%d 递归处截断后 Sink 应为 0，got %d", depth, got)
		}
	}
}
