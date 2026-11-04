package action

// T12b：续查预算统一为**全请求总追加次数**（每个前沿展开占 1 次；兄弟/
// 嵌套/回调共享；不是"一轮展开所有兄弟"）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

const t12bBudgetSrc = `package budget
func Leaf() {}
func Step() { Leaf() }
func One() int { return 1 }
func Entry() { Step(); Step(); Step() }
func EntryValue() { n:=One(); if n==1 { Leaf() } else { Step() } }
`

func seedT12bBudget(t *testing.T) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/budget\n\ngo 1.26.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "flow.go"), []byte(t12bBudgetSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	pkg := "symbol:go:example.com/budget:"
	line := func(name string) int {
		for i, l := range strings.Split(t12bBudgetSrc, "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "func "+name+"(") {
				return i + 1
			}
		}
		return 0
	}
	nodes := []*domain.CodeEntity{
		{ID: domain.CanonicalID(pkg + "Leaf"), Kind: domain.KindFunction, Name: "Leaf", FilePath: "flow.go", LineStart: line("Leaf")},
		{ID: domain.CanonicalID(pkg + "Step"), Kind: domain.KindFunction, Name: "Step", FilePath: "flow.go", LineStart: line("Step")},
		{ID: domain.CanonicalID(pkg + "Entry"), Kind: domain.KindFunction, Name: "Entry", FilePath: "flow.go", LineStart: line("Entry")},
		{ID: domain.CanonicalID(pkg + "One"), Kind: domain.KindFunction, Name: "One", FilePath: "flow.go", LineStart: line("One")},
		{ID: domain.CanonicalID(pkg + "EntryValue"), Kind: domain.KindFunction, Name: "EntryValue", FilePath: "flow.go", LineStart: line("EntryValue")},
	}
	facts := []*domain.Fact{
		{SourceID: domain.CanonicalID(pkg + "Step"), TargetID: domain.CanonicalID(pkg + "Leaf"),
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": line("Step")}},
	}
	for i := 0; i < 3; i++ {
		facts = append(facts, &domain.Fact{SourceID: domain.CanonicalID(pkg + "Entry"),
			TargetID: domain.CanonicalID(pkg + "Step"), Kind: domain.FactCalls, Confidence: 0.8,
			Metadata: map[string]any{"line_num": line("Entry")}})
	}
	facts = append(facts,
		&domain.Fact{SourceID: domain.CanonicalID(pkg + "EntryValue"),
			TargetID: domain.CanonicalID(pkg + "One"), Kind: domain.FactCalls, Confidence: 0.8,
			Metadata: map[string]any{"line_num": line("EntryValue")}},
		&domain.Fact{SourceID: domain.CanonicalID(pkg + "EntryValue"),
			TargetID: domain.CanonicalID(pkg + "Leaf"), Kind: domain.FactCalls, Confidence: 0.8,
			Metadata: map[string]any{"line_num": line("EntryValue")}},
		&domain.Fact{SourceID: domain.CanonicalID(pkg + "EntryValue"),
			TargetID: domain.CanonicalID(pkg + "Step"), Kind: domain.FactCalls, Confidence: 0.8,
			Metadata: map[string]any{"line_num": line("EntryValue")}},
	)
	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatalf("save: %v", err)
	}
	return New(r), dir
}

func t12bMeta(meta *SeqContinuationMeta) (int, int, int, int) {
	if meta == nil {
		return -1, -1, -1, -1
	}
	return meta.Expanded, meta.ExtraExpansions, meta.SegmentsUsed, meta.BudgetStops
}

// 预算 1：只追加第一个 Step；其 Leaf 与另外两个 Step 保留 depth 停止。
func TestT12bBudgetOne(t *testing.T) {
	acts, dir := seedT12bBudget(t)
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/budget:Entry", RepoAbs: dir, Depth: 1,
		ContinueSegments: 1, Evidence: true})
	if err != nil {
		t.Fatal(err)
	}
	if root.Meta == nil || root.Meta.Continuation == nil {
		t.Fatalf("缺少 continuation 元数据")
	}
	expanded, extra, used, stops := t12bMeta(root.Meta.Continuation)
	if expanded != 1 || extra != 1 || used != 1 || stops != 3 {
		t.Fatalf("预算1元数据 = expanded:%d extra:%d used:%d stops:%d，want 1/1/1/3",
			expanded, extra, used, stops)
	}
	// 实际展开：恰一个 Step 有子节点（Leaf），另外两个 Step 仍停在 depth。
	steps := seqFindNodes(root, func(n *CodeSeqNode) bool {
		return n.Kind == "call" && n.Label == "Step"
	})
	if len(steps) != 3 {
		t.Fatalf("Step 节点 = %d, want 3", len(steps))
	}
	expandedSteps, stoppedSteps := 0, 0
	for _, n := range steps {
		if len(n.Nodes) > 0 {
			expandedSteps++
		}
		if n.Stop != nil && n.Stop.Code == SeqStopDepthLimit {
			stoppedSteps++
		}
	}
	if expandedSteps != 1 || stoppedSteps != 2 {
		t.Fatalf("实际展开 Step=%d 停止 Step=%d, want 1/2", expandedSteps, stoppedSteps)
	}
	leaves := seqFindNodes(root, func(n *CodeSeqNode) bool {
		return n.Kind == "call" && n.Label == "Leaf"
	})
	if len(leaves) != 1 {
		t.Fatalf("Leaf 节点 = %d, want 1（只来自已追加的 Step）", len(leaves))
	}
	if leaves[0].Stop == nil || leaves[0].Stop.Code != SeqStopDepthLimit {
		t.Errorf("Leaf 应保留 depth 停止: %+v", leaves[0].Stop)
	}
}

// 预算 2：追加前两个 Step（FIFO），第三个 Step 与两个 Leaf 仍停止。
func TestT12bBudgetTwo(t *testing.T) {
	acts, dir := seedT12bBudget(t)
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/budget:Entry", RepoAbs: dir, Depth: 1,
		ContinueSegments: 2, Evidence: true})
	if err != nil {
		t.Fatal(err)
	}
	expanded, extra, used, stops := t12bMeta(root.Meta.Continuation)
	if expanded != 2 || extra != 2 || used != 2 || stops != 3 {
		t.Fatalf("预算2元数据 = %d/%d/%d/%d，want 2/2/2/3", expanded, extra, used, stops)
	}
	if got := len(seqCallNodesNamed(root, "Leaf")); got != 2 {
		t.Fatalf("Leaf 节点 = %d, want 2", got)
	}
	// 两种入口预算相同 → 同一展开口径（Compat 只改身份，不改语义）。
	compat, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/budget:Entry", RepoAbs: dir, Depth: 1,
		Continue: &SeqContinuationOptions{MaxSegments: 2}, Evidence: true})
	if err != nil {
		t.Fatal(err)
	}
	ce, cx, cu, cs := t12bMeta(compat.Meta.Continuation)
	if ce != expanded || cx != extra || cu != used || cs != stops {
		t.Fatalf("两种入口预算口径不同: %d/%d/%d/%d vs %d/%d/%d/%d",
			ce, cx, cu, cs, expanded, extra, used, stops)
	}
	if !compat.Meta.Continuation.CompatMode {
		t.Error("兼容入口应标记 compat_mode")
	}
}

// 身份变化先占用预算但不计追加次数；剩余前沿保留 depth 预算停止。
func TestT12bIdentityChangedConsumesBudget(t *testing.T) {
	req := continueReq(1)
	req.acc = &seqTimingAcc{}
	first := depthStopNode("symbol:go:example.com/m:First")
	second := depthStopNode("symbol:go:example.com/m:Second")
	seqRegisterFrontier(req, first, "symbol:go:example.com/m:First", func(CodeSequenceRequest) {
		t.Error("身份变化时不应展开")
	}, first.Stop.Detail)
	seqRegisterFrontier(req, second, "symbol:go:example.com/m:Second", func(CodeSequenceRequest) {
		t.Error("预算已被身份变化占用，不得展开第二个前沿")
	}, second.Stop.Detail)
	first.Evidence.SymbolID = "symbol:go:example.com/m:Other"
	seqContinueSegments(New(nil), req, &CodeSeqNode{})
	meta := seqContinuationMeta(req)
	if meta.Expanded != 0 || meta.ExtraExpansions != 0 || meta.SegmentsUsed != 0 {
		t.Fatalf("身份变化不得计追加次数: expanded=%d extra=%d used=%d",
			meta.Expanded, meta.ExtraExpansions, meta.SegmentsUsed)
	}
	if meta.BudgetStops != 1 {
		t.Fatalf("剩余前沿应保留 depth 预算停止: %+v", meta)
	}
	sawChanged := false
	for _, f := range meta.Frontiers {
		if f.IdentityChanged {
			sawChanged = true
		}
	}
	if !sawChanged {
		t.Fatalf("应记录 identity_changed: %+v", meta.Frontiers)
	}
}

// 语句结束即续查：后续 if 能看到 One() 的返回槽（字面量 1），只走 Leaf。
func TestT12bInlineCallPointBinding(t *testing.T) {
	acts, dir := seedT12bBudget(t)
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/budget:EntryValue", RepoAbs: dir, Depth: 1,
		ContinueSegments: 1, Evidence: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(seqCallNodesNamed(root, "Leaf")); got != 1 {
		t.Fatalf("续查后 n==1 应走 Leaf（got %d）: %v", got, labelsOf(root.Nodes))
	}
	if got := len(seqCallNodesNamed(root, "Step")); got != 0 {
		t.Fatalf("不应走 else 的 Step（got %d）", got)
	}
	if root.Meta == nil || root.Meta.Continuation == nil ||
		root.Meta.Continuation.Expanded != 1 {
		t.Fatalf("应恰好追加 1 次: %+v", root.Meta)
	}
}
