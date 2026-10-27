package action

// todo22 §3.2 测试：接口多候选不选第一个展开、候选预算/截断诊断；
// 有准确构造证据（到达定义）时才具体化到对应实现。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedIfaceRepo 播种接口 + N 个实现（implements 边：接口 → 实现者）。
func seedIfaceRepo(t *testing.T, src string, impls []string) (*Actions, string) {
	t.Helper()
	dir := writeSeqFixture(t, src)
	// 行号按真实 AST（避免手写行号与源码不一致导致定位失败）
	fset := token.NewFileSet()
	f, perr := parser.ParseFile(fset, "flow.go", src, 0)
	if perr != nil {
		t.Fatalf("parse fixture: %v", perr)
	}
	lineOf := func(name, recv string) int {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Name.Name != name {
				continue
			}
			if recv == "" && fd.Recv != nil {
				continue
			}
			if recv != "" && (fd.Recv == nil || len(fd.Recv.List) == 0 ||
				recvTypeName(fd.Recv.List[0].Type) != recv) {
				continue
			}
			return fset.Position(fd.Pos()).Line
		}
		return 1
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:Run", Kind: domain.KindFunction, Name: "Run", FilePath: "flow.go", LineStart: lineOf("Run", "")},
		{ID: "symbol:go:example.com/m:Doer", Kind: domain.KindInterface, Name: "Doer"},
		{ID: "symbol:go:example.com/m:(Doer).Do", Kind: domain.KindMethod, Name: "(Doer).Do"},
	}
	var edges []*domain.Fact
	for _, impl := range impls {
		nodes = append(nodes,
			&domain.CodeEntity{ID: domain.CanonicalID("symbol:go:example.com/m:" + impl),
				Kind: domain.KindStruct, Name: impl, FilePath: "flow.go", LineStart: 1},
			&domain.CodeEntity{ID: domain.CanonicalID("symbol:go:example.com/m:(" + impl + ").Do"),
				Kind: domain.KindMethod, Name: "(" + impl + ").Do", FilePath: "flow.go", LineStart: lineOf("Do", impl)})
		edges = append(edges, &domain.Fact{
			SourceID: "symbol:go:example.com/m:Doer", TargetID: domain.CanonicalID("symbol:go:example.com/m:" + impl),
			Kind: domain.FactImplements, ToolSource: domain.ToolSCIP, Confidence: 1})
	}
	// 接口方法调用边（生产由 AST 适配器发出：Run → 接口方法，带 pos/line）
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "Run" || fd.Body == nil {
			continue
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Do" {
				return true
			}
			edges = append(edges, &domain.Fact{
				SourceID: "symbol:go:example.com/m:Run",
				TargetID: "symbol:go:example.com/m:(Doer).Do",
				Kind:     domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.8,
				Metadata: map[string]any{
					"line_num": fset.Position(call.Lparen).Line,
					"pos":      fset.Position(call.Lparen).Offset,
				}})
			return true
		})
	}
	if _, err := r.SaveBatchStats(nodes, edges, nil); err != nil {
		t.Fatalf("save: %v", err)
	}
	return New(r), dir
}

// TestSeqIfaceCandidatesNotExpanded：无消歧证据 → 保留候选、不展开、不选第一个。
func TestSeqIfaceCandidatesNotExpanded(t *testing.T) {
	src := `package m

type Doer interface{ Do() }

type A struct{}
type B struct{}

func (a *A) Do() { OnlyA() }
func (b *B) Do() { OnlyB() }

func OnlyA() {}
func OnlyB() {}

func Run(d Doer) { d.Do() }
`
	acts, dir := seedIfaceRepo(t, src, []string{"A", "B"})
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Run", RepoAbs: dir, Depth: 3})
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}
	calls := seqCallNodesNamed(root, "d.Do")
	if len(calls) != 1 {
		t.Fatalf("应有 1 个 d.Do 调用节点，got %d", len(calls))
	}
	n := calls[0]
	if n.Stop == nil || n.Stop.Code != SeqStopIfaceAmbiguous {
		t.Errorf("多候选未消歧应给 %s，got %+v", SeqStopIfaceAmbiguous, n.Stop)
	}
	if n.Evidence == nil || !n.Evidence.Ambiguous || len(n.Evidence.Candidates) != 2 {
		t.Fatalf("应保留完整候选集合（2 个）+ ambiguous，got %+v", n.Evidence)
	}
	if n.ImplType != "" || n.Evidence.SymbolID != "" {
		t.Errorf("未消歧时不得制造具体实现（ImplType=%q SymbolID=%q）", n.ImplType, n.Evidence.SymbolID)
	}
	if !strings.Contains(n.Evidence.CandidateBudget, "候选深度 0") {
		t.Errorf("应输出候选预算/截断原因，got %q", n.Evidence.CandidateBudget)
	}
	// 两个实现的函数体都不得被展开
	if got := seqCallNodesNamed(n, "OnlyA"); len(got) != 0 {
		t.Errorf("未消歧不得展开候选实现内部调用，got %d 个 OnlyA", len(got))
	}
	if got := seqCallNodesNamed(n, "OnlyB"); len(got) != 0 {
		t.Errorf("未消歧不得展开候选实现内部调用，got %d 个 OnlyB", len(got))
	}
}

// TestSeqIfaceCandidateBudgetTruncation：候选 >12 → 截断并明确标注。
func TestSeqIfaceCandidateBudgetTruncation(t *testing.T) {
	impls := make([]string, 0, 14)
	src := "package m\n\ntype Doer interface{ Do() }\n\nfunc Run(d Doer) { d.Do() }\n\n"
	for i := 0; i < 14; i++ {
		name := "Impl" + string(rune('A'+i))
		impls = append(impls, name)
		src += "type " + name + " struct{}\n\nfunc (x *" + name + ") Do() {}\n\n"
	}
	acts, dir := seedIfaceRepo(t, src, impls)
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Run", RepoAbs: dir, Depth: 3})
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}
	calls := seqCallNodesNamed(root, "d.Do")
	if len(calls) != 1 || calls[0].Evidence == nil {
		t.Fatalf("应有 d.Do 候选证据，got %+v", calls)
	}
	ev := calls[0].Evidence
	if len(ev.Candidates) != seqMaxIfaceCandidates {
		t.Errorf("候选应截断为 %d，got %d", seqMaxIfaceCandidates, len(ev.Candidates))
	}
	if !strings.Contains(ev.CandidateBudget, "已截断") {
		t.Errorf("截断应明确标注，got %q", ev.CandidateBudget)
	}
}

// TestSeqIfaceProvenImplExpanded：到达定义能证明唯一实现 → 具体化并展开。
func TestSeqIfaceProvenImplExpanded(t *testing.T) {
	src := `package m

type Doer interface{ Do() }

type A struct{}
type B struct{}

func (a *A) Do() { OnlyA() }
func (b *B) Do() { OnlyB() }

func OnlyA() {}
func OnlyB() {}

func Run() {
	var d Doer = &A{}
	d.Do()
}
`
	acts, dir := seedIfaceRepo(t, src, []string{"A", "B"})
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Run", RepoAbs: dir, Depth: 3})
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}
	calls := seqCallNodesNamed(root, "d.Do")
	if len(calls) != 1 {
		t.Fatalf("应有 1 个 d.Do，got %d", len(calls))
	}
	n := calls[0]
	if n.ImplType == "" && (n.Evidence == nil || n.Evidence.SymbolID == "") {
		t.Fatalf("有构造证据时应具体化到 A.Do，got %+v", n.Evidence)
	}
	if got := seqCallNodesNamed(n, "OnlyA"); len(got) != 1 {
		t.Errorf("应展开 A.Do 的函数体（OnlyA 一次），got %d", len(got))
	}
	if got := seqCallNodesNamed(n, "OnlyB"); len(got) != 0 {
		t.Errorf("不得展开未被证明的 B.Do，got %d", len(got))
	}
}
