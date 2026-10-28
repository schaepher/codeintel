//go:build integration

package integration

// todo25 §3.4 剩余边界：嵌入提升方法、闭包捕获后修改、未知方法改写。
// 全部真实 init → CLI；证明不足时不得借用字段证据。

import (
	"testing"

	"github.com/schaepher/codeintel/internal/action"
)

const todo25FactoryCore = `package continuation

type API interface{ Work() }
type One struct{}
type Two struct{}
func (*One) Work(){ OneEvent() }
func (*Two) Work(){ TwoEvent() }
func OneEvent(){}
func TwoEvent(){}
type Base struct{ worker API }
func (b *Base) Set(v API){ b.worker = v }
func (b *Base) Get() API { return b.worker }
type Box struct{ *Base }
func (b *Box) Run(){ b.Step() }
func (b *Box) Step(){ b.Get().Work() }
`

const todo25FactoryEmbeddedFlow = todo25FactoryCore + `
func Build(kind int) (*Box, error) {
	box := &Box{}
	switch kind {
	case 1:
		box.Set(&One{})
	case 2:
		box.Set(&Two{})
	default:
		return nil, nil
	}
	return box, nil
}
func Entry(){ b, err := Build(1); if err == nil { b.Run() } }
`

const todo25FactoryClosureFlow = todo25FactoryCore + `
func Build(kind int) (*Box, error) {
	box := &Box{}
	apply := func(v API){ box.Set(v) }
	switch kind {
	case 1:
		apply(&One{})
	case 2:
		apply(&Two{})
	default:
		return nil, nil
	}
	return box, nil
}
func Entry(){ b, err := Build(1); if err == nil { b.Run() } }
`

const todo25FactoryUnknownFlow = todo25FactoryCore + `
func (b *Box) Rewrite(){ /* 未证明是否改写 worker */ }
func Build(kind int) (*Box, error) {
	box := &Box{}
	switch kind {
	case 1:
		box.Set(&One{})
	case 2:
		box.Set(&Two{})
	}
	box.Rewrite()
	return box, nil
}
func Entry(){ b, err := Build(1); if err == nil { b.Run() } }
`

func todo25FactoryFieldEvidence(root *action.CodeSeqNode) *action.CodeSeqNode {
	var hit *action.CodeSeqNode
	walkSeq(root, func(n *action.CodeSeqNode) {
		if n.Label == "b.Get().Work" {
			hit = n
		}
	})
	return hit
}

func TestTodo25FactoryEmbeddedPromotion(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := todo25ContinuationRepo(t, todo25FactoryEmbeddedFlow)
	root := seqTreeJSON(t, dir, "symbol:go:example.com/continuation:Entry", 24)
	parent := todo25FactoryFieldEvidence(root)
	if parent == nil {
		t.Fatalf("缺少 b.Get().Work 节点: %+v", root)
	}
	if parent.Evidence == nil || parent.Evidence.Source != action.SeqEvidenceFieldFlow ||
		len(parent.Evidence.Candidates) != 2 || !parent.Evidence.Ambiguous {
		t.Fatalf("嵌入提升方法应保留两条字段候选: %+v", parent.Evidence)
	}
	if countSeqSym(root, ":OneEvent") != 1 || countSeqSym(root, ":TwoEvent") != 1 {
		t.Errorf("嵌入场景各自实现事件应各一次: One=%d Two=%d",
			countSeqSym(root, ":OneEvent"), countSeqSym(root, ":TwoEvent"))
	}
}

func TestTodo25FactoryClosureCapture(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := todo25ContinuationRepo(t, todo25FactoryClosureFlow)
	root := seqTreeJSON(t, dir, "symbol:go:example.com/continuation:Entry", 24)
	parent := todo25FactoryFieldEvidence(root)
	if parent == nil {
		t.Fatalf("缺少 b.Get().Work 节点: %+v", root)
	}
	if parent.Evidence == nil || parent.Evidence.Source != action.SeqEvidenceFieldFlow {
		t.Fatalf("闭包内 setter 也应证明字段来源: %+v", parent.Evidence)
	}
	if len(parent.Evidence.Candidates) != 2 {
		t.Errorf("闭包内两个 setter 实参应保留两条候选: %+v", parent.Evidence)
	}
	guards := map[string]bool{}
	for _, br := range parent.Nodes {
		guards[br.Label] = true
	}
	if !guards["kind == 1"] || !guards["kind == 2"] {
		t.Errorf("闭包调用仍应保留 kind 守卫，got %v", guards)
	}
	if countSeqSym(root, ":OneEvent") != 1 || countSeqSym(root, ":TwoEvent") != 1 {
		t.Errorf("闭包场景事件应各一次: One=%d Two=%d",
			countSeqSym(root, ":OneEvent"), countSeqSym(root, ":TwoEvent"))
	}
}

func TestTodo25FactoryUnknownMethodRewrite(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := todo25ContinuationRepo(t, todo25FactoryUnknownFlow)
	out := seqTreeJSON(t, dir, "symbol:go:example.com/continuation:Entry", 24)
	walkSeq(out, func(n *action.CodeSeqNode) {
		if n.Evidence != nil && n.Evidence.Source == action.SeqEvidenceFieldFlow &&
			len(n.Evidence.Candidates) > 0 {
			t.Errorf("未知方法可能改写字段时不得借用字段证据: %+v", n.Evidence)
		}
	})
	if countSeqSym(out, ":OneEvent") != 0 || countSeqSym(out, ":TwoEvent") != 0 {
		t.Errorf("未知方法改写后不应执行具体实现: One=%d Two=%d",
			countSeqSym(out, ":OneEvent"), countSeqSym(out, ":TwoEvent"))
	}
}
