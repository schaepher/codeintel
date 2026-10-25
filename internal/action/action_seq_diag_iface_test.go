package action

// todo10-3：接口派发多候选的证据（候选集合 + ambiguous）——从
// action_seq_diag_test.go 拆出（行数治理）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// TestSeqIfaceAmbiguous：接口方法多实现时保留候选集合 + ambiguous（不静默
// 选定一个实现当结论）。
func TestSeqIfaceAmbiguous(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package m

type Doer interface {
	Do() error
}

type ImplA struct{}

func (ImplA) Do() error { return nil }

type ImplB struct{}

func (ImplB) Do() error { return nil }

type Svc struct{}

func Run(d Doer) { d.Do() }
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:Run", Kind: domain.KindFunction, Name: "Run", FilePath: "main.go", LineStart: 17},
		{ID: "symbol:go:example.com/m:Doer", Kind: domain.KindInterface, Name: "Doer"},
		{ID: "symbol:go:example.com/m:(Doer).Do", Kind: domain.KindMethod, Name: "(Doer).Do"},
		{ID: "symbol:go:example.com/m:ImplA", Kind: domain.KindStruct, Name: "ImplA"},
		{ID: "symbol:go:example.com/m:(ImplA).Do", Kind: domain.KindMethod, Name: "(ImplA).Do", FilePath: "main.go", LineStart: 9},
		{ID: "symbol:go:example.com/m:ImplB", Kind: domain.KindStruct, Name: "ImplB"},
		{ID: "symbol:go:example.com/m:(ImplB).Do", Kind: domain.KindMethod, Name: "(ImplB).Do", FilePath: "main.go", LineStart: 13},
	}
	facts := []*domain.Fact{
		// 索引里调用边指向**接口方法**（未具体化）→ 时序展开必须走
		// implements 边做候选，并标 ambiguous
		{SourceID: "symbol:go:example.com/m:Run", TargetID: "symbol:go:example.com/m:(Doer).Do",
			Kind: domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.8, Metadata: map[string]any{"line_num": float64(17)}},
		// 两个实现 → implements 边
		{SourceID: "symbol:go:example.com/m:Doer", TargetID: "symbol:go:example.com/m:ImplA",
			Kind: domain.FactImplements, ToolSource: domain.ToolSCIP, Confidence: 1.0},
		{SourceID: "symbol:go:example.com/m:Doer", TargetID: "symbol:go:example.com/m:ImplB",
			Kind: domain.FactImplements, ToolSource: domain.ToolSCIP, Confidence: 1.0},
	}
	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatal(err)
	}
	root, err := New(r).CodeSequence(CodeSequenceRequest{Target: "example.com/m:Run", RepoAbs: dir, Depth: 2})
	if err != nil || root == nil {
		t.Fatalf("CodeSequence: %v root=%v", err, root)
	}
	var call *CodeSeqNode
	for _, n := range root.Nodes {
		if n.Kind == "call" {
			call = n
		}
	}
	if call == nil {
		t.Fatalf("应有调用节点: %+v", root.Nodes)
	}
	ev := call.Evidence
	if ev == nil || ev.Source != SeqEvidenceIface {
		t.Fatalf("接口派发应有 iface 证据: %+v", ev)
	}
	if len(ev.Candidates) < 2 {
		t.Errorf("应保留候选集合（%d 个）: %+v", len(ev.Candidates), ev)
	}
	if !ev.Ambiguous {
		t.Error("多候选必须标 ambiguous（不得静默选择一个实现）")
	}
	if !strings.HasPrefix(ev.Reason, "first-of-") {
		t.Errorf("依据应说明选中规则: %+v", ev)
	}
}
