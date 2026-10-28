package action

// T04：构造器/字段/包级单例/条件工厂的数据流边界。
//
// 覆盖：接口返回的实际 return 来源（不按名字猜）、未知 Inject 写者、
// 同字段多具体写者 + 未知写者、builtin new(T)、跨包 init 注入、switch
// 返回接口的有界形态。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// t04Repo 写入文件（可多文件/多模块目录）并建索引节点/边。
type t04Repo struct {
	dir   string
	files map[string]string
}

func seedT04(t *testing.T, files map[string]string, nodes []*domain.CodeEntity, facts []*domain.Fact,
	sums ...*domain.FunctionFieldSummary) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := files["go.mod"]; !ok {
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.26.2\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats(nodes, facts, sums); err != nil {
		t.Fatalf("save: %v", err)
	}
	return New(r), dir
}

// t04LineOf 源码中包含 substr 的行号（夹具行号不硬编码）。
func t04LineOf(t *testing.T, src, substr string) int {
	t.Helper()
	for i, l := range strings.Split(src, "\n") {
		if strings.Contains(l, substr) {
			return i + 1
		}
	}
	t.Fatalf("fixture 找不到 %q", substr)
	return 0
}

// t04Node 函数节点（行号由源码定位）。
func t04Func(t *testing.T, pkg, file, src, name string) *domain.CodeEntity {
	t.Helper()
	line := 0
	for i, l := range strings.Split(src, "\n") {
		if strings.Contains(l, "func "+name+"(") || strings.Contains(l, "func ("+name+")") {
			line = i + 1
			break
		}
	}
	if line == 0 {
		t.Fatalf("fixture 找不到函数 %s", name)
	}
	return &domain.CodeEntity{ID: domain.CanonicalID("symbol:go:" + pkg + ":" + name),
		Kind: domain.KindFunction, Name: name, FilePath: file, LineStart: line}
}

// 无写者/唯一实现的对照正例：工厂 return &Real{}（接口返回）→ Real.Work。
func TestT04ConstructorReturnSource(t *testing.T) {
	src := `package m

type API interface{ Work() }
type Real struct{}
type Other struct{}

func (*Real) Work()  { RealHit() }
func (*Other) Work() { OtherHit() }
func RealHit()       {}
func OtherHit()      {}

func newService() API { return &Real{} }

func Entry() { newService().Work() }
`
	pkg := "example.com/m"
	acts, dir := seedT04(t, map[string]string{"main.go": src}, []*domain.CodeEntity{
		{ID: domain.CanonicalID("symbol:go:" + pkg + ":API"), Kind: domain.KindInterface, Name: "API",
			Properties: map[string]any{"methods": "Work"}},
		{ID: domain.CanonicalID("symbol:go:" + pkg + ":(API).Work"), Kind: domain.KindMethod, Name: "(API).Work"},
		{ID: domain.CanonicalID("symbol:go:" + pkg + ":Real"), Kind: domain.KindStruct, Name: "Real"},
		{ID: domain.CanonicalID("symbol:go:" + pkg + ":Other"), Kind: domain.KindStruct, Name: "Other"},
		{ID: domain.CanonicalID("symbol:go:" + pkg + ":(Real).Work"), Kind: domain.KindMethod, Name: "(Real).Work", FilePath: "main.go", LineStart: 7},
		{ID: domain.CanonicalID("symbol:go:" + pkg + ":(Other).Work"), Kind: domain.KindMethod, Name: "(Other).Work", FilePath: "main.go", LineStart: 8},
		t04Func(t, pkg, "main.go", src, "RealHit"),
		t04Func(t, pkg, "main.go", src, "OtherHit"),
		t04Func(t, pkg, "main.go", src, "newService"),
		t04Func(t, pkg, "main.go", src, "Entry"),
	}, []*domain.Fact{
		{SourceID: domain.CanonicalID("symbol:go:" + pkg + ":Entry"),
			TargetID: domain.CanonicalID("symbol:go:" + pkg + ":newService"),
			Kind:     domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": t04LineOf(t, src, "newService().Work()")}},
		{SourceID: domain.CanonicalID("symbol:go:" + pkg + ":Entry"),
			TargetID: domain.CanonicalID("symbol:go:" + pkg + ":(API).Work"),
			Kind:     domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": t04LineOf(t, src, "newService().Work()")}},
		{SourceID: domain.CanonicalID("symbol:go:" + pkg + ":(Real).Work"),
			TargetID: domain.CanonicalID("symbol:go:" + pkg + ":RealHit"),
			Kind:     domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": 7}},
		{SourceID: domain.CanonicalID("symbol:go:" + pkg + ":(Other).Work"),
			TargetID: domain.CanonicalID("symbol:go:" + pkg + ":OtherHit"),
			Kind:     domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": 8}},
		{SourceID: domain.CanonicalID("symbol:go:" + pkg + ":API"),
			TargetID: domain.CanonicalID("symbol:go:" + pkg + ":Real"), Kind: domain.FactImplements, Confidence: 1.0},
		{SourceID: domain.CanonicalID("symbol:go:" + pkg + ":API"),
			TargetID: domain.CanonicalID("symbol:go:" + pkg + ":Other"), Kind: domain.FactImplements, Confidence: 1.0},
	})
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "symbol:go:" + pkg + ":Entry", RepoAbs: dir, Depth: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(seqCallNodesNamed(root, "RealHit")) != 1 {
		t.Errorf("实际 return 来源 Real 应展开（RealHit=1）: %v", labelsOf(root.Nodes))
	}
	if len(seqCallNodesNamed(root, "OtherHit")) != 0 {
		t.Errorf("名字相似/未返回的 Other 不得参与: %v", labelsOf(root.Nodes))
	}
}

// 包级单例 + init 注入：准确实现；新增未知 Inject 写者后不维持唯一实现。
func TestT04SingletonUnknownWriter(t *testing.T) {
	base := `package m

type API interface{ Work() }
type Impl struct{}

func (*Impl) Work() { Hit() }
func Hit()          {}

var current API

func Inject(v API) { current = v }

func init() { Inject(&Impl{}) }

func Get() API { return current }

func Entry() { Get().Work() }
`
	extra := `
func reload(v API) { current = v }
func init2()       { reload(unknownAPI()) }
`
	pkg := "example.com/m"
	for _, tc := range []struct {
		name    string
		src     string
		wantHit int
	}{
		{"proven", base, 1},
		{"unknown-writer", base + extra, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nodes := []*domain.CodeEntity{
				{ID: domain.CanonicalID("symbol:go:" + pkg + ":API"), Kind: domain.KindInterface, Name: "API"},
				{ID: domain.CanonicalID("symbol:go:" + pkg + ":(API).Work"), Kind: domain.KindMethod, Name: "(API).Work"},
				{ID: domain.CanonicalID("symbol:go:" + pkg + ":Impl"), Kind: domain.KindStruct, Name: "Impl"},
				{ID: domain.CanonicalID("symbol:go:" + pkg + ":(Impl).Work"), Kind: domain.KindMethod, Name: "(Impl).Work",
					FilePath: "main.go", LineStart: 6},
				t04Func(t, pkg, "main.go", tc.src, "Hit"),
				t04Func(t, pkg, "main.go", tc.src, "Inject"),
				t04Func(t, pkg, "main.go", tc.src, "Get"),
				t04Func(t, pkg, "main.go", tc.src, "Entry"),
				t04Func(t, pkg, "main.go", tc.src, "init"),
			}
			for _, extraFn := range []string{"reload", "init2"} {
				if strings.Contains(tc.src, "func "+extraFn+"(") {
					nodes = append(nodes, t04Func(t, pkg, "main.go", tc.src, extraFn))
				}
			}
			facts := []*domain.Fact{
				{SourceID: domain.CanonicalID("symbol:go:" + pkg + ":(Impl).Work"),
					TargetID: domain.CanonicalID("symbol:go:" + pkg + ":Hit"),
					Kind:     domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": 6}},
				{SourceID: domain.CanonicalID("symbol:go:" + pkg + ":API"),
					TargetID: domain.CanonicalID("symbol:go:" + pkg + ":Impl"), Kind: domain.FactImplements, Confidence: 1.0},
				{SourceID: domain.CanonicalID("symbol:go:" + pkg + ":init"),
					TargetID: domain.CanonicalID("symbol:go:" + pkg + ":Inject"),
					Kind:     domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": t04LineOf(t, tc.src, "Inject(&Impl{})")}},
				{SourceID: domain.CanonicalID("symbol:go:" + pkg + ":Entry"),
					TargetID: domain.CanonicalID("symbol:go:" + pkg + ":Get"),
					Kind:     domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": t04LineOf(t, tc.src, "Get().Work()")}},
				{SourceID: domain.CanonicalID("symbol:go:" + pkg + ":Entry"),
					TargetID: domain.CanonicalID("symbol:go:" + pkg + ":(API).Work"),
					Kind:     domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": t04LineOf(t, tc.src, "Get().Work()")}},
			}
			if strings.Contains(tc.src, "init2") {
				facts = append(facts, &domain.Fact{SourceID: domain.CanonicalID("symbol:go:" + pkg + ":init2"),
					TargetID: domain.CanonicalID("symbol:go:" + pkg + ":reload"),
					Kind:     domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": t04LineOf(t, tc.src, "reload(unknownAPI())")}})
			}
			acts, dir := seedT04(t, map[string]string{"main.go": tc.src}, nodes, facts)
			root, err := acts.CodeSequence(CodeSequenceRequest{Target: "symbol:go:" + pkg + ":Entry", RepoAbs: dir, Depth: 4})
			if err != nil {
				t.Fatal(err)
			}
			if got := len(seqCallNodesNamed(root, "Hit")); got != tc.wantHit {
				t.Errorf("Hit=%d, want %d（未知写者不得维持唯一实现）", got, tc.wantHit)
			}
		})
	}
}

// builtin new(T) 来源：new(Impl) 赋给接口字段 → 具体实现。
func TestT04BuiltinNewSource(t *testing.T) {
	src := `package m

type API interface{ Work() }
type Impl struct{}

func (*Impl) Work() { Hit() }
func Hit()          {}

type Svc struct{ api API }

func NewSvc() *Svc {
	s := &Svc{}
	s.api = new(Impl)
	return s
}

func (s *Svc) Run() { s.api.Work() }
`
	pkg := "example.com/m"
	acts, dir := seedT04(t, map[string]string{"main.go": src}, []*domain.CodeEntity{
		{ID: domain.CanonicalID("symbol:go:" + pkg + ":API"), Kind: domain.KindInterface, Name: "API"},
		{ID: domain.CanonicalID("symbol:go:" + pkg + ":(API).Work"), Kind: domain.KindMethod, Name: "(API).Work"},
		{ID: domain.CanonicalID("symbol:go:" + pkg + ":Impl"), Kind: domain.KindStruct, Name: "Impl"},
		{ID: domain.CanonicalID("symbol:go:" + pkg + ":(Impl).Work"), Kind: domain.KindMethod, Name: "(Impl).Work", FilePath: "main.go", LineStart: 6},
		{ID: domain.CanonicalID("symbol:go:" + pkg + ":Svc"), Kind: domain.KindStruct, Name: "Svc"},
		{ID: domain.CanonicalID("symbol:go:" + pkg + ":(Svc).Run"), Kind: domain.KindMethod,
			Name: "(Svc).Run", FilePath: "main.go", LineStart: t04LineOf(t, src, "func (s *Svc) Run")},
		t04Func(t, pkg, "main.go", src, "Hit"),
		t04Func(t, pkg, "main.go", src, "NewSvc"),
	}, []*domain.Fact{
		{SourceID: domain.CanonicalID("symbol:go:" + pkg + ":(Svc).Run"),
			TargetID: domain.CanonicalID("symbol:go:" + pkg + ":(API).Work"),
			Kind:     domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": t04LineOf(t, src, "s.api.Work()")}},
		{SourceID: domain.CanonicalID("symbol:go:" + pkg + ":(Impl).Work"),
			TargetID: domain.CanonicalID("symbol:go:" + pkg + ":Hit"),
			Kind:     domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": 6}},
		{SourceID: domain.CanonicalID("symbol:go:" + pkg + ":API"),
			TargetID: domain.CanonicalID("symbol:go:" + pkg + ":Impl"), Kind: domain.FactImplements, Confidence: 1.0},
	}, &domain.FunctionFieldSummary{
		FunctionID: domain.CanonicalID("symbol:go:" + pkg + ":NewSvc"),
		AccessKind: domain.SummaryDirectWrite, FieldPath: "example.com/m.Svc.api",
		InstancePath: "s.api", LineStart: t04LineOf(t, src, "s.api = new(Impl)"),
	})
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "symbol:go:" + pkg + ":(Svc).Run", RepoAbs: dir, Depth: 4})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(seqCallNodesNamed(root, "Hit")); got != 1 {
		t.Errorf("builtin new(Impl) 来源应正确定位（Hit=%d）", got)
	}
}
