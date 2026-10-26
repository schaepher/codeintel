package action

// todo20-9：同一函数重复调用同一目标——索引 calls 边按 (caller,target,
// kind) 汇总，只有首个调用点带 pos/line metadata。后续调用点不能因此
// 判成 unresolved：按本函数目标集合 + 调用名回退，并保留位置/诊断。
// 同时回归同层连续调用、分支、循环后调用与真正递归（按当前路径防环）。

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

const seqRepeatSrc = `package m

func Save() {}

func Runner(n int) {
	Save()
	Save()
	if n > 0 {
		Save()
	} else {
		Save()
	}
	for i := 0; i < n; i++ {
		Save()
	}
	Recur(n)
}

func Recur(n int) {
	if n > 0 {
		Recur(n - 1)
	}
}
`

func seedSeqRepeat(t *testing.T) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(seqRepeatSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:Save", Kind: domain.KindFunction, Name: "Save",
			FilePath: "main.go", LineStart: 3},
		{ID: "symbol:go:example.com/m:Runner", Kind: domain.KindFunction, Name: "Runner",
			FilePath: "main.go", LineStart: 5},
		{ID: "symbol:go:example.com/m:Recur", Kind: domain.KindFunction, Name: "Recur",
			FilePath: "main.go", LineStart: 19},
	}
	// 每种 (caller,target) 只有一条边——重复调用点的 metadata 已合并。
	facts := []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:Runner", TargetID: "symbol:go:example.com/m:Save",
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": float64(6)}},
		{SourceID: "symbol:go:example.com/m:Runner", TargetID: "symbol:go:example.com/m:Recur",
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": float64(16)}},
		{SourceID: "symbol:go:example.com/m:Recur", TargetID: "symbol:go:example.com/m:Recur",
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": float64(21)}},
	}
	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "seqrepeat", ToolName: "test", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	return New(r), dir
}

// collectSeqCalls 收集树上的调用节点（含 Else/递归展开）。
func collectSeqCalls(n *CodeSeqNode) []*CodeSeqNode {
	if n == nil {
		return nil
	}
	var out []*CodeSeqNode
	if n.Kind == "call" {
		out = append(out, n)
	}
	for _, c := range n.Nodes {
		out = append(out, collectSeqCalls(c)...)
	}
	for _, c := range n.Else {
		out = append(out, collectSeqCalls(c)...)
	}
	return out
}

func seqDiagCount(code string, diags []SeqDiagCount) int {
	for _, d := range diags {
		if d.Code == code {
			return d.Count
		}
	}
	return 0
}

func TestCodeSequenceRepeatedCallsResolvedByAggregatedEdge(t *testing.T) {
	acts, dir := seedSeqRepeat(t)
	const saveID = "symbol:go:example.com/m:Save"
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/m:Runner", RepoAbs: dir, Depth: 1})
	if err != nil || root == nil {
		t.Fatalf("CodeSequence %v root=%v", err, root)
	}
	var saveNodes []*CodeSeqNode
	for _, n := range collectSeqCalls(root) {
		if n.Label == "Save" {
			saveNodes = append(saveNodes, n)
		}
	}
	wantLines := []int{6, 7, 9, 11, 14}
	if len(saveNodes) != len(wantLines) {
		t.Fatalf("应有 %d 个 Save 调用点，实为 %d", len(wantLines), len(saveNodes))
	}
	for i, n := range saveNodes {
		if n.Line != wantLines[i] {
			t.Errorf("第 %d 个 Save 行号 = %d，期望 %d", i, n.Line, wantLines[i])
		}
		if n.Evidence == nil || n.Evidence.SymbolID != saveID {
			t.Errorf("行 %d 的目标身份缺失: %+v", n.Line, n.Evidence)
			continue
		}
		if n.Stop == nil || n.Stop.Code != SeqStopDepthLimit {
			t.Errorf("行 %d 应因 depth 停止，实为 %+v", n.Line, n.Stop)
		}
		wantReason := "index-call-edge"
		if i > 0 {
			wantReason = SeqReasonAggregatedCallEdge
		}
		if n.Evidence.Reason != wantReason {
			t.Errorf("行 %d evidence.reason=%q，期望 %q", n.Line, n.Evidence.Reason, wantReason)
		}
		wantVerdict := SeqVerdictDirect
		if i > 0 {
			wantVerdict = SeqVerdictCorroborated
		}
		if n.Evidence.Verdict != wantVerdict {
			t.Errorf("行 %d verdict=%q，期望 %q", n.Line, n.Evidence.Verdict, wantVerdict)
		}
	}
	if got := seqDiagCount(SeqStopUnresolved, root.Meta.Diagnostics); got != 0 {
		t.Errorf("重复调用不得判为 unresolved: %+v", root.Meta.Diagnostics)
	}
	if got := seqDiagCount(SeqStopDepthLimit, root.Meta.Diagnostics); got != 6 {
		t.Errorf("depth=1 应 6 个调用点都因 depth 停止（含 Recur），实为 %d: %+v",
			got, root.Meta.Diagnostics)
	}
}

func TestCodeSequenceRepeatedCallsExpandAtDepth2(t *testing.T) {
	acts, dir := seedSeqRepeat(t)
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/m:Runner", RepoAbs: dir, Depth: 2})
	if err != nil || root == nil {
		t.Fatalf("CodeSequence %v root=%v", err, root)
	}
	for _, n := range collectSeqCalls(root) {
		if n.Label == "Save" {
			if n.Stop != nil && n.Stop.Code == SeqStopUnresolved {
				t.Errorf("行 %d 不应 unresolved: %+v", n.Line, n.Stop)
			}
			if len(n.Nodes) != 0 || n.Stop == nil || n.Stop.Code != SeqStopNoCalls {
				t.Errorf("行 %d 应展开 Save（空函数 → no_calls），实为 nodes=%d stop=%+v",
					n.Line, len(n.Nodes), n.Stop)
			}
		}
	}
	if got := seqDiagCount(SeqStopUnresolved, root.Meta.Diagnostics); got != 0 {
		t.Errorf("depth=2 不得有 unresolved: %+v", root.Meta.Diagnostics)
	}
}

func TestCodeSequenceRecursionStopsOnCurrentPath(t *testing.T) {
	acts, dir := seedSeqRepeat(t)
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/m:Runner", RepoAbs: dir, Depth: 4})
	if err != nil || root == nil {
		t.Fatalf("CodeSequence %v root=%v", err, root)
	}
	var cycles int
	for _, n := range collectSeqCalls(root) {
		if n.Label == "Recur" && n.Stop != nil && n.Stop.Code == SeqStopCycle {
			cycles++
		}
		if n.Stop != nil && n.Stop.Code == SeqStopUnresolved {
			t.Errorf("行 %d Recur 递归不应 unresolved: %+v", n.Line, n.Stop)
		}
	}
	if cycles == 0 {
		t.Errorf("递归展开应在当前路径上命中 cycle: %+v", root.Meta.Diagnostics)
	}
	if got := seqDiagCount(SeqStopUnresolved, root.Meta.Diagnostics); got != 0 {
		t.Errorf("递归不得因访问过目标而判 unresolved: %+v", root.Meta.Diagnostics)
	}
}
