package cli

// todo13-4：未知实现的 Mermaid 出口保留完整候选（JSON/文本/图同一身份）。
//   - 无场景时参与者标“实现未消歧”，不把首个实现画成已确定调用
//   - 图内 Note 列出 ImplCN.Do / ImplUS.Do

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

const seqImplCandidateSrc = `package m

type Doer interface{ Do() }

type ImplCN struct{}

func (ImplCN) Do() {}

type ImplUS struct{}

func (ImplUS) Do() {}

func Pick(platform string) {
	var d Doer
	if platform == "CN" {
		d = &ImplCN{}
	} else {
		d = &ImplUS{}
	}
	d.Do()
}
`

func seedSeqImplCandidate(t *testing.T) (*action.Actions, string) {
	t.Helper()
	dir := seedRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "pick.go"), []byte(seqImplCandidateSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:Pick", Kind: domain.KindFunction, Name: "Pick", FilePath: "pick.go", LineStart: 13},
		{ID: "symbol:go:example.com/m:Doer", Kind: domain.KindInterface, Name: "Doer"},
		{ID: "symbol:go:example.com/m:(Doer).Do", Kind: domain.KindMethod, Name: "(Doer).Do"},
		{ID: "symbol:go:example.com/m:ImplCN", Kind: domain.KindStruct, Name: "ImplCN"},
		{ID: "symbol:go:example.com/m:ImplUS", Kind: domain.KindStruct, Name: "ImplUS"},
		{ID: "symbol:go:example.com/m:(ImplCN).Do", Kind: domain.KindMethod, Name: "(ImplCN).Do", FilePath: "pick.go", LineStart: 7},
		{ID: "symbol:go:example.com/m:(ImplUS).Do", Kind: domain.KindMethod, Name: "(ImplUS).Do", FilePath: "pick.go", LineStart: 11},
	}
	facts := []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:Pick", TargetID: "symbol:go:example.com/m:(Doer).Do",
			Kind: domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.8,
			Metadata: map[string]any{"line_num": float64(20)}},
		{SourceID: "symbol:go:example.com/m:Doer", TargetID: "symbol:go:example.com/m:ImplCN",
			Kind: domain.FactImplements, ToolSource: domain.ToolSCIP, Confidence: 1.0},
		{SourceID: "symbol:go:example.com/m:Doer", TargetID: "symbol:go:example.com/m:ImplUS",
			Kind: domain.FactImplements, ToolSource: domain.ToolSCIP, Confidence: 1.0},
	}
	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatal(err)
	}
	return action.New(r), dir
}

func TestSeqMermaidUnknownImplCandidates(t *testing.T) {
	acts, dir := seedSeqImplCandidate(t)
	root, err := acts.CodeSequence(action.CodeSequenceRequest{Target: "Pick", RepoAbs: dir, Depth: 3})
	if err != nil || root == nil {
		t.Fatalf("CodeSequence: %v root=%v", err, root)
	}
	var call *action.CodeSeqNode
	var walk func([]*action.CodeSeqNode)
	walk = func(ns []*action.CodeSeqNode) {
		for _, n := range ns {
			if n.Kind == "call" && n.Label == "d.Do" {
				call = n
			}
			walk(n.Nodes)
			walk(n.Else)
		}
	}
	walk(root.Nodes)
	if call == nil {
		t.Fatalf("未找到 d.Do: %+v", root)
	}
	if call.ImplType != "" {
		t.Errorf("未消歧时不得把首个实现当作确定参与者: %q", call.ImplType)
	}
	if call.Evidence == nil || !call.Evidence.Ambiguous || len(call.Evidence.Candidates) != 2 {
		t.Fatalf("应保留两项候选且 ambiguous: %+v", call.Evidence)
	}
	if len(call.Nodes) != 0 {
		t.Errorf("未消歧不得展开单一实现: %+v", call.Nodes)
	}
	m := renderCodeSeqMermaid(root)
	for _, want := range []string{"实现未消歧", "ImplCN.Do", "ImplUS.Do", "unverified"} {
		if !strings.Contains(m, want) {
			t.Errorf("mermaid 应含 %q:\n%s", want, m)
		}
	}
	if strings.Contains(m, "ImplCN<br/>") {
		t.Errorf("参与者不得显示成单一实现:\n%s", m)
	}
	txt := seqText(root.Nodes, 1)
	for _, want := range []string{"候选未消歧", "ImplCN.Do", "ImplUS.Do"} {
		if !strings.Contains(txt, want) {
			t.Errorf("文本应含 %q:\n%s", want, txt)
		}
	}
	// 指定场景仍唯一选择（不画候选 Note）
	root, err = acts.CodeSequence(action.CodeSequenceRequest{Target: "Pick", RepoAbs: dir,
		Depth: 3, Scene: action.NewScene([]string{"platform=US"})})
	if err != nil || root == nil {
		t.Fatalf("scene CodeSequence: %v", err)
	}
	m = renderCodeSeqMermaid(root)
	if strings.Contains(m, "实现未消歧") {
		t.Errorf("US 场景已唯一确定，不应再标未消歧:\n%s", m)
	}
	if !strings.Contains(m, "ImplUS") {
		t.Errorf("US 场景应选 ImplUS:\n%s", m)
	}
	if strings.Contains(m, "对 ImplCN 的确定调用") || strings.Contains(m, "ImplCN.Do") {
		t.Errorf("US 场景不得把 ImplCN 画成候选/调用:\n%s", m)
	}
}
