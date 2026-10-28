package action

// T12：未验证实现候选的后代不得被自动续查（候选不是确定执行；预算停止也不
// 计到候选内部前沿）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

func seedT12Cand(t *testing.T) (*Actions, string) {
	t.Helper()
	src := `package m

type API interface{ Do() }

type AImpl struct{}
type BImpl struct{}

func (*AImpl) Do() { DeepA() }
func (*BImpl) Do() { DeepB() }

func DeepA() { DeeperA() }
func DeepB() { DeeperB() }
func DeeperA() {}
func DeeperB() {}

func Entry(v API) { v.Do() }
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.26.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	pkg := "symbol:go:example.com/m:"
	line := func(prefix string) int {
		for i, l := range strings.Split(src, "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), prefix) {
				return i + 1
			}
		}
		return 0
	}
	nodes := []*domain.CodeEntity{
		{ID: domain.CanonicalID(pkg + "API"), Kind: domain.KindInterface, Name: "API"},
		{ID: domain.CanonicalID(pkg + "(API).Do"), Kind: domain.KindMethod, Name: "(API).Do"},
		{ID: domain.CanonicalID(pkg + "AImpl"), Kind: domain.KindStruct, Name: "AImpl"},
		{ID: domain.CanonicalID(pkg + "BImpl"), Kind: domain.KindStruct, Name: "BImpl"},
	}
	for _, m := range []struct{ name, key string }{
		{"(AImpl).Do", "func (*AImpl) Do()"}, {"(BImpl).Do", "func (*BImpl) Do()"},
	} {
		nodes = append(nodes, &domain.CodeEntity{ID: domain.CanonicalID(pkg + m.name),
			Kind: domain.KindMethod, Name: m.name, FilePath: "main.go", LineStart: line(m.key)})
	}
	for _, fn := range []string{"DeepA", "DeepB", "DeeperA", "DeeperB", "Entry"} {
		nodes = append(nodes, &domain.CodeEntity{ID: domain.CanonicalID(pkg + fn),
			Kind: domain.KindFunction, Name: fn, FilePath: "main.go", LineStart: line("func " + fn + "(")})
	}
	var facts []*domain.Fact
	for _, f := range []struct{ src, dst, key string }{
		{"Entry", "(API).Do", "v.Do()"},
		{"(AImpl).Do", "DeepA", "DeepA()"},
		{"(BImpl).Do", "DeepB", "DeepB()"},
		{"DeepA", "DeeperA", "DeeperA()"},
		{"DeepB", "DeeperB", "DeeperB()"},
	} {
		facts = append(facts, &domain.Fact{SourceID: domain.CanonicalID(pkg + f.src),
			TargetID: domain.CanonicalID(pkg + f.dst), Kind: domain.FactCalls,
			Confidence: 0.8, Metadata: map[string]any{"line_num": line(f.key)}})
	}
	for _, impl := range []string{"AImpl", "BImpl"} {
		facts = append(facts, &domain.Fact{SourceID: domain.CanonicalID(pkg + "API"),
			TargetID: domain.CanonicalID(pkg + impl), Kind: domain.FactImplements, Confidence: 1.0})
	}
	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatalf("save: %v", err)
	}
	return New(r), dir
}

// 候选分支会被展开（≤2 层），但候选内部停在 depth 的调用**不得**登记为续查
// 前沿（自动续查只针对确定执行的前沿）。
func TestT12CandidateDescendantsNotContinued(t *testing.T) {
	acts, dir := seedT12Cand(t)
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/m:Entry", RepoAbs: dir, Depth: 2, ContinueSegments: 4})
	if err != nil {
		t.Fatal(err)
	}
	// 候选分支存在且后代标 Candidate
	cands := seqFindNodes(root, func(n *CodeSeqNode) bool { return n.Candidate })
	if len(cands) == 0 {
		t.Fatalf("应有未验证实现候选分支: %v", labelsOf(root.Nodes))
	}
	if root.Meta == nil || root.Meta.Continuation == nil {
		return // 没有前沿也是合法结果（候选内容不续查）
	}
	for _, f := range root.Meta.Continuation.Frontiers {
		if strings.Contains(f.TargetID, "Deep") || strings.Contains(f.TargetID, "Deeper") {
			t.Errorf("候选后代不得被自动续查: %+v", f)
		}
	}
	// 候选内部深层调用不应出现在续查展开结果里（没有候选后代被续查）
	if got := len(seqCallNodesNamed(root, "DeeperA")) + len(seqCallNodesNamed(root, "DeeperB")); got != 0 {
		t.Errorf("候选后代不得被续查展开（Deeper=%d）", got)
	}
}

// 单元口径：inCandidate 时登记前沿是 no-op。
func TestT12RegisterFrontierInCandidateNoop(t *testing.T) {
	req := continueReq(2)
	req.acc = &seqTimingAcc{}
	req.inCandidate = true
	node := depthStopNode("symbol:go:example.com/m:First")
	seqRegisterFrontier(req, node, "symbol:go:example.com/m:First", func(CodeSequenceRequest) {
		t.Error("候选内前沿不得被续查")
	}, node.Stop.Detail)
	if len(req.acc.frontiers) != 0 {
		t.Fatalf("inCandidate 时不应登记前沿: %+v", req.acc.frontiers)
	}
	seqContinueSegments(New(nil), req, &CodeSeqNode{})
	if req.acc.continueExpansions != 0 {
		t.Errorf("候选内前沿不应计入续查次数")
	}
}
