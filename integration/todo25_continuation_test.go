//go:build integration

package integration

// todo25 自动续查剩余边界（第 3/4 项）：未验证接口候选不续查、覆写/预算/
// 循环/defer 回归。真实 init → CLI；公开输入逐字取自 todo25.txt。

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
)

const todo25AmbiguousFlow = `package continuation

type Worker interface { Work() }
type A struct{}
type B struct{}
func (A) Work(){ A1() }
func (B) Work(){ B1() }
func A1(){ A2() }
func B1(){ B2() }
func A2(){ HitA() }
func B2(){ HitB() }
func HitA(){}
func HitB(){}
func AmbiguousEntry(){ var worker Worker; worker.Work() }
`

const todo25OverwriteFlow = `package continuation

func Entry(){ f:=func(){Sink()}; First(f); f=func(){Wrong()}; _=f }
func First(f func()){Second(f)}
func Second(f func()){Execute(f)}
func Execute(f func()){f()}
func Sink(){}
func Wrong(){}
func LoopEntry(){Again(func(){Wrong()})}
func Again(f func()){Again(f)}
func DeferredEntry(){defer First(func(){Sink()});go First(func(){Sink()})}
`

func todo25ContinuationRepo(t *testing.T, flow string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/continuation\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), flow)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "init")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit=%d", code)
	}
	return dir
}

func TestTodo25AmbiguousCandidatesNotContinued(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := todo25ContinuationRepo(t, todo25AmbiguousFlow)
	root := seqTreeJSON(t, dir, "symbol:go:example.com/continuation:AmbiguousEntry", 3,
		"--continue-segments", "16", "--evidence")
	if countSeqSym(root, ":HitA") != 0 || countSeqSym(root, ":HitB") != 0 {
		t.Errorf("未验证候选内部调用不得续查: HitA=%d HitB=%d",
			countSeqSym(root, ":HitA"), countSeqSym(root, ":HitB"))
	}
	var work *action.CodeSeqNode
	walkSeq(root, func(n *action.CodeSeqNode) {
		if n.Evidence != nil && n.Evidence.Source == action.SeqEvidenceIface {
			work = n
		}
	})
	if work == nil {
		t.Fatalf("应保留 worker.Work 节点")
	}
	if work.Stop == nil || work.Stop.Code != action.SeqStopIfaceAmbiguous {
		t.Errorf("未验证候选应保留 iface_ambiguous: %+v", work.Stop)
	}
	if !work.Evidence.Ambiguous || len(work.Evidence.Candidates) != 2 {
		t.Errorf("应保留两个候选及 ambiguous: %+v", work.Evidence)
	}
	if work.Evidence.CandidateDepth > 2 {
		t.Errorf("候选展开深度不得超过两层: %d", work.Evidence.CandidateDepth)
	}
	if root.Meta == nil || root.Meta.Continuation == nil {
		t.Fatalf("缺少 meta.continuation")
	}
	c := root.Meta.Continuation
	if c.SegmentsUsed != 0 || c.BudgetStops != 0 || len(c.Frontiers) != 0 {
		t.Errorf("候选未证明时续查数必须为 0: %+v", c)
	}
}

func TestTodo25OverwriteAndBudgetRegression(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := todo25ContinuationRepo(t, todo25OverwriteFlow)
	entry := "symbol:go:example.com/continuation:Entry"

	// 原参数绑定不被调用后的覆写污染。
	full := seqTreeJSON(t, dir, entry, 1, "--continue-segments", "16", "--timeout", "20s")
	if countSeqSym(full, ":Sink") != 1 || countSeqSym(full, ":Wrong") != 0 {
		t.Errorf("覆写回归: Sink=%d Wrong=%d; want 1/0",
			countSeqSym(full, ":Sink"), countSeqSym(full, ":Wrong"))
	}

	// 预算 1：Sink 零、budget_stops=1；同一前沿经两层登记不重复计数。
	one := seqTreeJSON(t, dir, entry, 1, "--continue-segments", "1")
	if countSeqSym(one, ":Sink") != 0 {
		t.Errorf("预算 1 不应展开到 Sink")
	}
	if one.Meta == nil || one.Meta.Continuation == nil || one.Meta.Continuation.BudgetStops != 1 {
		t.Errorf("预算 1 应 budget_stops=1: %+v", one.Meta)
	}
	if one.Meta != nil && one.Meta.Continuation != nil {
		var stopped int
		for _, f := range one.Meta.Continuation.Frontiers {
			if !f.Continued {
				stopped++
			}
		}
		if stopped != 1 {
			t.Errorf("同一前沿只能计一次预算停止，got %d: %+v", stopped, one.Meta.Continuation.Frontiers)
		}
	}

	// 循环：cycle=1、Wrong=0（不靠耗尽段数）。
	loop := seqTreeJSON(t, dir, "symbol:go:example.com/continuation:LoopEntry", 1,
		"--continue-segments", "16")
	if seqDiagnosticCount(loop, "cycle") != 1 || countSeqSym(loop, ":Wrong") != 0 {
		t.Errorf("LoopEntry: cycle=%d Wrong=%d; want 1/0",
			seqDiagnosticCount(loop, "cycle"), countSeqSym(loop, ":Wrong"))
	}

	// defer/go：Sink=2，标记保留、不伪造完成顺序。
	deferred := seqTreeJSON(t, dir, "symbol:go:example.com/continuation:DeferredEntry", 1,
		"--continue-segments", "16")
	if countSeqSym(deferred, ":Sink") != 2 {
		t.Errorf("DeferredEntry Sink=%d; want 2", countSeqSym(deferred, ":Sink"))
	}
	var execs []string
	for _, n := range deferred.Nodes {
		execs = append(execs, n.Exec)
	}
	if len(execs) != 2 || execs[0] != "defer" || execs[1] != "async" {
		t.Errorf("defer/async 标记应保留: %v", execs)
	}
}

const todo25FactoryFlow = `package continuation

type API interface{ Work() }
type One struct{}
type Two struct{}
func (*One) Work(){ OneEvent() }
func (*Two) Work(){ TwoEvent() }
func OneEvent(){}
func TwoEvent(){}
type Box struct{ worker API }
func (b *Box) Set(v API){ b.worker = v }
func (b *Box) Get() API { return b.worker }
func (b *Box) Run(){ b.Step() }
func (b *Box) Step(){ b.Get().Work() }
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

// TestTodo25FactoryFieldContinuation 验收 §3.4 公开输入：保留两条 kind 守卫及
// 各自 (One).Work/(Two).Work 准确 ID；字段来源证据；不串行宣称同时执行。
func TestTodo25FactoryFieldContinuation(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := todo25ContinuationRepo(t, todo25FactoryFlow)
	root := seqTreeJSON(t, dir, "symbol:go:example.com/continuation:Entry", 24)
	var parent *action.CodeSeqNode
	walkSeq(root, func(n *action.CodeSeqNode) {
		if n.Label == "b.Get().Work" {
			parent = n
		}
	})
	if parent == nil {
		t.Fatalf("缺少 b.Get().Work 节点: %+v", root)
	}
	if parent.Evidence == nil || parent.Evidence.Source != action.SeqEvidenceFieldFlow {
		t.Fatalf("字段来源证据缺失: %+v", parent.Evidence)
	}
	if !parent.Evidence.Ambiguous || len(parent.Evidence.Candidates) != 2 {
		t.Errorf("两条条件实现应保留候选且不唯一: %+v", parent.Evidence)
	}
	wantCands := map[string]bool{
		"symbol:go:example.com/continuation:(One).Work": true,
		"symbol:go:example.com/continuation:(Two).Work": true,
	}
	for _, c := range parent.Evidence.Candidates {
		if !wantCands[c] {
			t.Errorf("候选 ID 不准确: %s", c)
		}
		delete(wantCands, c)
	}
	if len(wantCands) != 0 {
		t.Errorf("缺候选: %v", wantCands)
	}
	guards := map[string]bool{}
	for _, br := range parent.Nodes {
		if br.Kind != "branch" || len(br.Nodes) != 1 {
			t.Errorf("条件实现应为互斥分支: %+v", br)
			continue
		}
		guards[br.Label] = true
	}
	if !guards["kind == 1"] || !guards["kind == 2"] {
		t.Errorf("应保留两条 kind 守卫，got %v", guards)
	}
	if countSeqSym(root, ":OneEvent") != 1 || countSeqSym(root, ":TwoEvent") != 1 {
		t.Errorf("各自实现的事件应各一次: One=%d Two=%d",
			countSeqSym(root, ":OneEvent"), countSeqSym(root, ":TwoEvent"))
	}
}

// TestTodo25FactoryFieldNegative：getter 返回其他字段时不得借用 worker 证据。
func TestTodo25FactoryFieldNegative(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	flow := strings.Replace(todo25FactoryFlow,
		"func (b *Box) Get() API { return b.worker }",
		"func (b *Box) Get() API { return b.other }", 1)
	flow = strings.Replace(flow, "type Box struct{ worker API }",
		"type Box struct{ worker API; other API }", 1)
	if flow == todo25FactoryFlow {
		t.Fatal("fixture 替换失效")
	}
	dir := todo25ContinuationRepo(t, flow)
	root := seqTreeJSON(t, dir, "symbol:go:example.com/continuation:Entry", 24)
	walkSeq(root, func(n *action.CodeSeqNode) {
		if n.Evidence != nil && n.Evidence.Source == action.SeqEvidenceFieldFlow &&
			len(n.Evidence.Candidates) > 0 {
			t.Errorf("getter 返回其他字段不得借用 worker 证据: %+v", n.Evidence)
		}
	})
	if countSeqSym(root, ":OneEvent") != 0 || countSeqSym(root, ":TwoEvent") != 0 {
		t.Errorf("未知字段不应执行具体实现: One=%d Two=%d",
			countSeqSym(root, ":OneEvent"), countSeqSym(root, ":TwoEvent"))
	}
}
