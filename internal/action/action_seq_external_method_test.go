package action

// todo22 P1 测试：外部依赖的**方法调用**（extT.Method()）、外部包内部跨包
// 调用目标、go.work 形态的外部模块——都按需解析源码（离线只读）并给出
// 准确外部目标 ID；标准库不按外部依赖展开。

import (
	"go/ast"
	"os"
	"path/filepath"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedExtMethodRepo 建 app（索引内）+ ext（仓库外模块，replace）。
func seedExtMethodRepo(t *testing.T, appSrc, extSrc string) (*Actions, string) {
	t.Helper()
	root := t.TempDir()
	app := filepath.Join(root, "app")
	ext := filepath.Join(root, "ext")
	for _, d := range []string{app, ext} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	gomod := "module example.com/app\n\ngo 1.21\n\nrequire example.com/ext v0.0.0\n\nreplace example.com/ext => ../ext\n"
	if err := os.WriteFile(filepath.Join(app, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "flow.go"), []byte(appSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ext, "go.mod"), []byte("module example.com/ext\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ext, "ext.go"), []byte(extSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	return indexAppOnly(t, app, appSrc), app
}

// seedGoWorkRepo 建 app + shared（go.work 工作区；app 的 go.mod 不 require）。
func seedGoWorkRepo(t *testing.T, appSrc, sharedSrc string) (*Actions, string) {
	t.Helper()
	root := t.TempDir()
	app := filepath.Join(root, "app")
	shared := filepath.Join(root, "shared")
	for _, d := range []string{app, shared} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(rel, body string) {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.work", "go 1.21\n\nuse (\n\t./app\n\t./shared\n)\n")
	write("app/go.mod", "module example.com/app\n\ngo 1.21\n")
	write("app/flow.go", appSrc)
	write("shared/go.mod", "module example.com/shared\n\ngo 1.21\n")
	write("shared/s.go", sharedSrc)
	return indexAppOnly(t, app, appSrc), app
}

// indexAppOnly 只索引 app 包（外部模块不进索引）；返回 Actions。
func indexAppOnly(t *testing.T, appDir, appSrc string) *Actions {
	t.Helper()
	db, err := sqlite.Open(appDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	fset, f := parseSeqFile(t, appSrc)
	var nodes []*domain.CodeEntity
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		id := "symbol:go:example.com/app:" + fd.Name.Name
		kind := domain.KindFunction
		if fd.Recv != nil && len(fd.Recv.List) > 0 {
			id = "symbol:go:example.com/app:(" + recvTypeName(fd.Recv.List[0].Type) + ")." + fd.Name.Name
			kind = domain.KindMethod
		}
		nodes = append(nodes, &domain.CodeEntity{ID: domain.CanonicalID(id), Kind: kind,
			Name: fd.Name.Name, FilePath: "flow.go", LineStart: fset.Position(fd.Pos()).Line})
	}
	if _, err := r.SaveBatchStats(nodes, nil, nil); err != nil {
		t.Fatalf("save: %v", err)
	}
	return New(r)
}

const extMethodSrc = `package ext

import "context"

type Store struct{}

func (s *Store) Run(ctx context.Context, f func()) { f() }

func NewStore() *Store { return &Store{} }
`

// TestSeqExternalMethodCall：外部方法调用按需展开（声明类型 / 复合字面量 /
// 构造器返回三条来源），回调按形参绑定证明执行。
func TestSeqExternalMethodCall(t *testing.T) {
	cases := []struct {
		name string
		decl string
	}{
		{"var 声明类型", "var s ext.Store"},
		{"复合字面量", "s := &ext.Store{}"},
		{"构造器返回类型", "s := ext.NewStore()"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			appSrc := `package app

import "example.com/ext"

func Inner() {}

func Entry() {
	` + c.decl + `
	s.Run(nil, func() { Inner() })
}
`
			acts, app := seedExtMethodRepo(t, appSrc, extMethodSrc)
			root, err := acts.CodeSequence(CodeSequenceRequest{
				Target: "symbol:go:example.com/app:Entry", RepoAbs: app, Depth: 4})
			if err != nil {
				t.Fatalf("CodeSequence: %v", err)
			}
			runs := seqCallNodesNamed(root, "s.Run")
			if len(runs) != 1 {
				t.Fatalf("应有 1 个 s.Run 节点，got %d", len(runs))
			}
			ev := runs[0].Evidence
			if ev == nil || ev.Source != SeqEvidenceExternal {
				t.Fatalf("外部方法应按需解析源码，got %+v", ev)
			}
			if ev.SymbolID != "symbol:go:example.com/ext:(Store).Run" {
				t.Errorf("外部方法 ID 应准确，got %q", ev.SymbolID)
			}
			if got := seqCallNodesNamed(runs[0], "Inner"); len(got) != 1 {
				t.Errorf("回调内部 Inner 应执行一次，got %d", len(got))
			}
		})
	}
}

// TestSeqExternalMethodDepthOne：depth=1 保留准确外部方法 ID，不启动 go list。
func TestSeqExternalMethodDepthOne(t *testing.T) {
	appSrc := `package app

import "example.com/ext"

func Inner() {}

func Entry() {
	var s ext.Store
	s.Run(nil, func() { Inner() })
}
`
	acts, app := seedExtMethodRepo(t, appSrc, extMethodSrc)
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/app:Entry", RepoAbs: app, Depth: 1})
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}
	runs := seqCallNodesNamed(root, "s.Run")
	if len(runs) != 1 || runs[0].Evidence == nil {
		t.Fatalf("应有 1 个带证据的 s.Run，got %+v", runs)
	}
	if runs[0].Evidence.SymbolID != "symbol:go:example.com/ext:(Store).Run" {
		t.Errorf("depth=1 也要保留准确外部方法 ID，got %q", runs[0].Evidence.SymbolID)
	}
	if runs[0].Stop == nil || runs[0].Stop.Code != SeqStopDepthLimit {
		t.Errorf("depth=1 应给 depth 诊断，got %+v", runs[0].Stop)
	}
	if len(runs[0].Nodes) != 0 {
		t.Errorf("depth=1 不得展开外部源码，got %d 子节点", len(runs[0].Nodes))
	}
}

// TestSeqExternalCrossPackageInsideDep：外部包内部再调用它自己的子包——
// go list 以外部包的模块根为 cwd，目标仍能解析。
func TestSeqExternalCrossPackageInsideDep(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "app")
	ext := filepath.Join(root, "ext")
	for _, d := range []string{app, filepath.Join(ext, "util")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, body string) {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	appSrc := `package app

import "example.com/ext"

func Entry() { ext.Run() }
`
	write(filepath.Join(app, "go.mod"), "module example.com/app\n\ngo 1.21\n\nrequire example.com/ext v0.0.0\n\nreplace example.com/ext => ../ext\n")
	write(filepath.Join(app, "flow.go"), appSrc)
	write(filepath.Join(ext, "go.mod"), "module example.com/ext\n\ngo 1.21\n")
	write(filepath.Join(ext, "ext.go"), `package ext

import "example.com/ext/util"

func Run() { util.Do() }
`)
	write(filepath.Join(ext, "util", "util.go"), `package util

func Do() {}
`)
	acts := indexAppOnly(t, app, appSrc)
	rootSeq, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/app:Entry", RepoAbs: app, Depth: 5})
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}
	dos := seqCallNodesNamed(rootSeq, "util.Do")
	if len(dos) != 1 {
		t.Fatalf("外部包内部跨包调用应被解析（util.Do），got %d", len(dos))
	}
	ev := dos[0].Evidence
	if ev == nil || ev.SymbolID != "symbol:go:example.com/ext/util:Do" {
		t.Errorf("跨包目标 ID 应准确，got %+v", ev)
	}
}

// TestSeqExternalGoWorkModule：go.work 工作区里的外部模块（app 的 go.mod
// 没有 require）同样按需解析。
func TestSeqExternalGoWorkModule(t *testing.T) {
	appSrc := `package app

import "example.com/shared"

func Entry() { shared.Run() }
`
	acts, app := seedGoWorkRepo(t, appSrc, `package shared

func Run() { Called() }

func Called() {}
`)
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/app:Entry", RepoAbs: app, Depth: 5})
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}
	runs := seqCallNodesNamed(root, "shared.Run")
	if len(runs) != 1 || runs[0].Evidence == nil ||
		runs[0].Evidence.SymbolID != "symbol:go:example.com/shared:Run" {
		t.Fatalf("go.work 外部模块应被解析，got %+v", runs)
	}
	if got := seqCallNodesNamed(runs[0], "Called"); len(got) != 1 {
		t.Errorf("外部模块内部调用应展开（Called 一次），got %d", len(got))
	}
}

// TestSeqExternalMethodStdlibNotExpanded：标准库方法不按"外部依赖"展开
// （保持 unresolved 分类，避免 stdlib 噪声污染时序图）。
func TestSeqExternalMethodStdlibNotExpanded(t *testing.T) {
	appSrc := `package app

import "bytes"

func Entry() {
	var b bytes.Buffer
	b.WriteString("x")
}
`
	acts, app := seedExtMethodRepo(t, appSrc, extMethodSrc)
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/app:Entry", RepoAbs: app, Depth: 4})
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}
	for _, n := range seqCallNodesNamed(root, "b.WriteString") {
		if n.Evidence != nil && n.Evidence.Source == SeqEvidenceExternal {
			t.Errorf("标准库方法不应按外部依赖展开，got %+v", n.Evidence)
		}
	}
}
