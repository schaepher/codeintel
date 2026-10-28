package action

// T05/U07：工厂返回对象的字段续接——每个守卫分支的 Work 有准确 canonical ID
// 与 kind 守卫，参与者（发送方）稳定；反例（读别的字段/写别的对象/无 default/
// 未知方法改写）不得沿用旧字段证明。

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

type t05Variant struct {
	name string
	src  string
	// wantProof=false：必须不出现 (One).Work / (Two).Work 的工厂字段证明。
	wantProof bool
}

func t05Base(buildBody string, setBody string, getBody string, extra string) string {
	return `package flow
type API interface{Work()}
type One struct{}
type Two struct{}
func(*One)Work(){OneEvent()}
func(*Two)Work(){TwoEvent()}
func OneEvent(){}
func TwoEvent(){}
type Box struct{worker API; other API}
func(b *Box)Set(v API){` + setBody + `}
func(b *Box)Get()API{` + getBody + `}
func(b *Box)Run(){b.Step()}
func(b *Box)Step(){b.Get().Work()}
func NewOne()*One{return &One{}}
func NewTwo()*Two{return &Two{}}
func NewPick()API{if true{return &One{}};return &Two{}}
func Build(kind int)(*Box,error){
 box:=&Box{}
` + buildBody + `
 return box,nil
}
` + extra + `
func Entry(){box,err:=Build(1);if err!=nil{return};box.Run()}
`
}

// t05Fixtures 正例与反例矩阵（"修改前改变一个事实"的手法）。
func t05Fixtures() []t05Variant {
	good := `switch kind{case 1:box.Set(NewOne());case 2:box.Set(NewTwo());default:return nil,nil}`
	return []t05Variant{
		{"positive", t05Base(good, "b.worker=v", "return b.worker", ""), true},
		{"get-other-field", t05Base(good, "b.worker=v", "return b.other", ""), false},
		{"set-other-field", t05Base(good, "b.other=v", "return b.worker", ""), false},
		{"no-default", t05Base(`switch kind{case 1:box.Set(NewOne());case 2:box.Set(NewTwo())}`, "b.worker=v", "return b.worker", ""), false},
		{"unknown-method-writes", t05Base(`switch kind{case 1:box.Set(NewOne());case 2:box.Set(NewTwo());default:return nil,nil}
 box.Mutate(NewPick())`, "b.worker=v", "return b.worker", `
func(b *Box)Mutate(v API){b.worker=v}
`), false},
		{"setter-arg-not-ctor", t05Base(`switch kind{case 1:box.Set(NewPick());case 2:box.Set(NewPick());default:return nil,nil}`, "b.worker=v", "return b.worker", ""), false},
	}
}

// t05Lines 函数/方法声明行与调用行（索引行号必须与源码一致）。
type t05Lines struct {
	decl map[string]int
	call map[string]int // "Caller->Callee" → 行号
}

func t05Parse(t *testing.T, src string) t05Lines {
	t.Helper()
	fs := token.NewFileSet()
	f, err := parser.ParseFile(fs, "flow.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := t05Lines{decl: map[string]int{}, call: map[string]int{}}
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		name := fd.Name.Name
		if fd.Recv != nil && len(fd.Recv.List) > 0 {
			name = "(" + recvTypeName(fd.Recv.List[0].Type) + ")." + name
		}
		out.decl[name] = fs.Position(fd.Pos()).Line
		if fd.Body == nil {
			continue
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			callee := ""
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				callee = fn.Name
			case *ast.SelectorExpr:
				callee = fn.Sel.Name
			}
			if callee == "" {
				return true
			}
			key := name + "->" + callee
			if _, seen := out.call[key]; !seen {
				out.call[key] = fs.Position(call.Lparen).Line
			}
			return true
		})
	}
	return out
}

func seedT05(t *testing.T, src string) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/flow\n\ngo 1.26.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "flow.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	pkg := "symbol:go:example.com/flow:"
	ln := t05Parse(t, src)
	method := func(recv, name string) *domain.CodeEntity {
		return &domain.CodeEntity{ID: domain.CanonicalID(pkg + "(" + recv + ")." + name),
			Kind: domain.KindMethod, Name: "(" + recv + ")." + name, FilePath: "flow.go",
			LineStart: ln.decl["("+recv+")."+name]}
	}
	fn := func(name string) *domain.CodeEntity {
		return &domain.CodeEntity{ID: domain.CanonicalID(pkg + name), Kind: domain.KindFunction,
			Name: name, FilePath: "flow.go", LineStart: ln.decl[name]}
	}
	callLine := func(caller, callee string) int { return ln.call[caller+"->"+callee] }
	nodes := []*domain.CodeEntity{
		{ID: domain.CanonicalID(pkg + "API"), Kind: domain.KindInterface, Name: "API",
			Properties: map[string]any{"methods": "Work"}},
		{ID: domain.CanonicalID(pkg + "(API).Work"), Kind: domain.KindMethod, Name: "(API).Work"},
		{ID: domain.CanonicalID(pkg + "Box"), Kind: domain.KindStruct, Name: "Box"},
		{ID: domain.CanonicalID(pkg + "One"), Kind: domain.KindStruct, Name: "One"},
		{ID: domain.CanonicalID(pkg + "Two"), Kind: domain.KindStruct, Name: "Two"},
		method("Box", "Set"), method("Box", "Get"), method("Box", "Run"), method("Box", "Step"),
		method("One", "Work"), method("Two", "Work"),
		fn("NewOne"), fn("NewTwo"), fn("Build"), fn("Entry"), fn("OneEvent"), fn("TwoEvent"),
	}
	facts := []*domain.Fact{
		{SourceID: domain.CanonicalID(pkg + "Entry"), TargetID: domain.CanonicalID(pkg + "Build"),
			Kind: domain.FactCalls, Confidence: 0.8,
			Metadata: map[string]any{"line_num": callLine("Entry", "Build")}},
		{SourceID: domain.CanonicalID(pkg + "Entry"), TargetID: domain.CanonicalID(pkg + "(Box).Run"),
			Kind: domain.FactCalls, Confidence: 0.8,
			Metadata: map[string]any{"line_num": callLine("Entry", "Run")}},
		{SourceID: domain.CanonicalID(pkg + "(Box).Run"), TargetID: domain.CanonicalID(pkg + "(Box).Step"),
			Kind: domain.FactCalls, Confidence: 0.8,
			Metadata: map[string]any{"line_num": callLine("(Box).Run", "Step")}},
		{SourceID: domain.CanonicalID(pkg + "(Box).Step"), TargetID: domain.CanonicalID(pkg + "(Box).Get"),
			Kind: domain.FactCalls, Confidence: 0.8,
			Metadata: map[string]any{"line_num": callLine("(Box).Step", "Get")}},
		{SourceID: domain.CanonicalID(pkg + "(Box).Step"), TargetID: domain.CanonicalID(pkg + "(API).Work"),
			Kind: domain.FactCalls, Confidence: 0.8,
			Metadata: map[string]any{"line_num": callLine("(Box).Step", "Work")}},
		{SourceID: domain.CanonicalID(pkg + "Build"), TargetID: domain.CanonicalID(pkg + "(Box).Set"),
			Kind: domain.FactCalls, Confidence: 0.8,
			Metadata: map[string]any{"line_num": callLine("Build", "Set")}},
		{SourceID: domain.CanonicalID(pkg + "(One).Work"), TargetID: domain.CanonicalID(pkg + "OneEvent"),
			Kind: domain.FactCalls, Confidence: 0.8,
			Metadata: map[string]any{"line_num": callLine("(One).Work", "OneEvent")}},
		{SourceID: domain.CanonicalID(pkg + "(Two).Work"), TargetID: domain.CanonicalID(pkg + "TwoEvent"),
			Kind: domain.FactCalls, Confidence: 0.8,
			Metadata: map[string]any{"line_num": callLine("(Two).Work", "TwoEvent")}},
		{SourceID: domain.CanonicalID(pkg + "API"), TargetID: domain.CanonicalID(pkg + "One"),
			Kind: domain.FactImplements, Confidence: 1.0},
		{SourceID: domain.CanonicalID(pkg + "API"), TargetID: domain.CanonicalID(pkg + "Two"),
			Kind: domain.FactImplements, Confidence: 1.0},
	}
	// 字段摘要（SSA 产出）：Set 写 worker / Get 读 worker——工厂字段续接的
	// 数据源（不能只靠 AST 猜）。
	sums := []*domain.FunctionFieldSummary{
		{FunctionID: domain.CanonicalID(pkg + "(Box).Set"), AccessKind: domain.SummaryDirectWrite,
			FieldPath: "example.com/flow.Box.worker", InstancePath: "b.worker",
			LineStart: ln.decl["(Box).Set"]},
		{FunctionID: domain.CanonicalID(pkg + "(Box).Get"), AccessKind: domain.SummaryDirectRead,
			FieldPath: "example.com/flow.Box.worker", InstancePath: "b.worker",
			LineStart: ln.decl["(Box).Get"]},
	}
	if _, err := r.SaveBatchStats(nodes, facts, sums); err != nil {
		t.Fatalf("save: %v", err)
	}
	return New(r), dir
}

// t05WorkBranches 收集工厂字段证明的 Work 调用节点及其守卫标签。
func t05WorkBranches(root *CodeSeqNode) []struct {
	node  *CodeSeqNode
	guard string
} {
	var out []struct {
		node  *CodeSeqNode
		guard string
	}
	var walk func(n *CodeSeqNode, guard string)
	walk = func(n *CodeSeqNode, guard string) {
		if n == nil {
			return
		}
		if n.Kind == "branch" {
			guard = n.Label
		}
		if n.Kind == "call" && n.Evidence != nil && n.Evidence.Reason == "factory-field-dataflow" &&
			n.Evidence.SymbolID != "" && strings.HasSuffix(n.Evidence.SymbolID, ").Work") {
			out = append(out, struct {
				node  *CodeSeqNode
				guard string
			}{n, guard})
			return
		}
		for _, c := range n.Nodes {
			walk(c, guard)
		}
		for _, c := range n.Else {
			walk(c, guard)
		}
	}
	walk(root, "")
	return out
}

func TestFactoryFieldGuardedBranches(t *testing.T) {
	for _, c := range t05Fixtures() {
		t.Run(c.name, func(t *testing.T) {
			acts, dir := seedT05(t, c.src)
			root, err := acts.CodeSequence(CodeSequenceRequest{
				Target: "symbol:go:example.com/flow:Entry", RepoAbs: dir, Depth: 8})
			if err != nil {
				t.Fatalf("CodeSequence: %v", err)
			}
			got := t05WorkBranches(root)
			if !c.wantProof {
				if len(got) != 0 {
					t.Fatalf("反例不应保留工厂字段证明: %+v", got)
				}
				return
			}
			if len(got) == 0 {
				t.Fatalf("正例应有工厂字段分支")
			}
			seen := map[string]string{}
			for _, b := range got {
				id := b.node.Evidence.SymbolID
				seen[id] = b.guard
				// U07：参与者稳定（不能是空发送方）
				if b.node.Actor == "" {
					t.Errorf("%s 缺参与者（会渲染成空发送方）", id)
				}
				if b.node.Evidence.Verdict != SeqVerdictCorroborated {
					t.Errorf("%s verdict = %q, want corroborated", id, b.node.Evidence.Verdict)
				}
			}
			for _, want := range []string{"(One).Work", "(Two).Work"} {
				id := "symbol:go:example.com/flow:" + want
				if _, ok := seen[id]; !ok {
					t.Errorf("缺少 %s 的守卫分支: %v", want, got)
				}
			}
			// 每个 Work 的内部调用发送方是其自身节点（不能空）。
			for _, b := range got {
				for _, sub := range b.node.Nodes {
					if sub.Kind == "call" && sub.Actor == "" {
						t.Errorf("%s 内部调用缺发送方: %+v", b.node.Evidence.SymbolID, sub)
					}
				}
			}
		})
	}
}
