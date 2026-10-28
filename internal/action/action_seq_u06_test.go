package action

// U06：接口字段/接收者的未知实例来源不得借用"全库唯一实现"——保留接口
// 调用与未验证候选；有准确实例来源的正例仍选中对应实现。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

func seedU06(t *testing.T, src string, impls []string) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.26.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "flow.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	lines := u06DeclLines(t, src)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	pkg := "symbol:go:example.com/m:"
	nodes := []*domain.CodeEntity{
		{ID: domain.CanonicalID(pkg + "API"), Kind: domain.KindInterface, Name: "API"},
		{ID: domain.CanonicalID(pkg + "(API).Work"), Kind: domain.KindMethod, Name: "(API).Work"},
		{ID: domain.CanonicalID(pkg + "Service"), Kind: domain.KindStruct, Name: "Service"},
		{ID: domain.CanonicalID(pkg + "(Service).Entry"), Kind: domain.KindMethod, Name: "(Service).Entry",
			FilePath: "flow.go", LineStart: lines["(Service).Entry"]},
	}
	var facts []*domain.Fact
	for _, impl := range impls {
		nodes = append(nodes,
			&domain.CodeEntity{ID: domain.CanonicalID(pkg + impl), Kind: domain.KindStruct, Name: impl},
			&domain.CodeEntity{ID: domain.CanonicalID(pkg + "(" + impl + ").Work"),
				Kind: domain.KindMethod, Name: "(" + impl + ").Work", FilePath: "flow.go",
				LineStart: lines["("+impl+").Work"]},
			&domain.CodeEntity{ID: domain.CanonicalID(pkg + "(" + impl + ").Event"),
				Kind: domain.KindMethod, Name: "(" + impl + ").Event", FilePath: "flow.go",
				LineStart: lines["("+impl+").Event"]},
		)
		facts = append(facts,
			&domain.Fact{SourceID: domain.CanonicalID(pkg + "API"), TargetID: domain.CanonicalID(pkg + impl),
				Kind: domain.FactImplements, Confidence: 1.0},
			&domain.Fact{SourceID: domain.CanonicalID(pkg + "(" + impl + ").Work"),
				TargetID: domain.CanonicalID(pkg + "(" + impl + ").Event"),
				Kind:     domain.FactCalls, Confidence: 0.8,
				Metadata: map[string]any{"line_num": lines["("+impl+").Work"]}},
		)
	}
	// 调用边：Entry → 接口方法（静态接口身份）
	facts = append(facts, &domain.Fact{SourceID: domain.CanonicalID(pkg + "(Service).Entry"),
		TargetID: domain.CanonicalID(pkg + "(API).Work"),
		Kind:     domain.FactCalls, Confidence: 0.8,
		Metadata: map[string]any{"line_num": u06CallLine(t, src)}})
	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatalf("save: %v", err)
	}
	return New(r), dir
}

// u06DeclLines 函数声明行（索引行号必须与源码一致）。
func u06DeclLines(t *testing.T, src string) map[string]int {
	t.Helper()
	fs := token.NewFileSet()
	f, err := parser.ParseFile(fs, "flow.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		name := fd.Name.Name
		if fd.Recv != nil && len(fd.Recv.List) > 0 {
			name = "(" + recvTypeName(fd.Recv.List[0].Type) + ")." + name
		}
		out[name] = fs.Position(fd.Pos()).Line
	}
	return out
}

// u06CallLine 接口调用所在行（s.biz.Work）。
func u06CallLine(t *testing.T, src string) int {
	t.Helper()
	for i, line := range strings.Split(src, "\n") {
		if strings.Contains(line, "s.biz.Work") {
			return i + 1
		}
	}
	t.Fatal("fixture 缺少 s.biz.Work 调用")
	return 0
}

const u06Src = `package m

type API interface{ Work() }

type One struct{}
type Two struct{}

func (*One) Work() { Event() }
func (t *Two) Work() { TwoEvent() }

type Service struct{ biz API }

func (s *Service) Entry() { s.biz.Work() }
`

func seqU06Call(t *testing.T, acts *Actions, dir string) *CodeSeqNode {
	t.Helper()
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/m:(Service).Entry", RepoAbs: dir, Depth: 4})
	if err != nil {
		t.Fatal(err)
	}
	calls := seqFindNodes(root, func(n *CodeSeqNode) bool {
		return n.Kind == "call" && strings.HasSuffix(n.Label, ".Work")
	})
	if len(calls) != 1 {
		t.Fatalf("应有 1 个接口调用节点，got %d", len(calls))
	}
	return calls[0]
}

// 最小无写者夹具：全库唯一实现也不接成确定调用。
func TestU06UnknownReceiverUniqueImplNotExpanded(t *testing.T) {
	acts, dir := seedU06(t, u06Src, []string{"One"})
	call := seqU06Call(t, acts, dir)
	if call.Evidence == nil {
		t.Fatalf("节点 = %+v", call)
	}
	if call.Evidence.Reason != "unknown_receiver" || !call.Evidence.Ambiguous {
		t.Errorf("证据应标 unknown_receiver/unverified: %+v", call.Evidence)
	}
	if call.Evidence.SymbolID != "" || call.ImplType != "" {
		t.Errorf("不得借用唯一实现: %+v", call.Evidence)
	}
	if call.Stop == nil || call.Stop.Code != SeqStopUnknownReceiver {
		t.Errorf("应停在 unknown_receiver: %+v", call.Stop)
	}
	if len(seqCallNodesNamed(call, "Event")) != 0 {
		t.Error("未知实例来源不得展开实现内部调用（不计确定执行）")
	}
}

// 多实现对照：同样保持未验证边界（不是"候选歧义分支"，而是来源未知）。
func TestU06UnknownReceiverMultipleImpls(t *testing.T) {
	acts, dir := seedU06(t, u06Src, []string{"One", "Two"})
	call := seqU06Call(t, acts, dir)
	if call.Evidence == nil || call.Evidence.Reason != "unknown_receiver" {
		t.Fatalf("证据 = %+v", call.Evidence)
	}
	if len(call.Evidence.Candidates) != 0 {
		t.Errorf("来源未知不应列出候选实现: %+v", call.Evidence.Candidates)
	}
	if len(call.Nodes) != 0 {
		t.Error("不得展开任一实现")
	}
}
