package action

// todo24 §11.4：SymbolSource 携带声明所在文件的 imports（源文件顺序；无显式
// 别名时省略 alias；"."/"_" 原样保留；依赖源码与名称入口共用提取逻辑）。

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

const symbolSourceImportsSrc = `package flow

import (
	output "fmt"
	"context"
	. "math"
	_ "net/http/pprof"
)

type Value struct{ Ctx context.Context }

const Flag = Pi

var Current = Flag

func Entry() { output.Println(Flag) }
`

const symbolSourceNoImportsSrc = `package flow

func Plain() {}
`

func seedSymbolSourceImportsRepo(t *testing.T) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/flow\n\ngo 1.26.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "flow.go"), []byte(symbolSourceImportsSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plain.go"), []byte(symbolSourceNoImportsSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/flow:Entry", Kind: domain.KindFunction, Name: "Entry", FilePath: "flow.go", LineStart: 999},
		{ID: "symbol:go:example.com/flow:Value", Kind: domain.KindStruct, Name: "Value", FilePath: "flow.go", LineStart: 999},
		{ID: "symbol:go:example.com/flow:Flag", Kind: domain.KindObject, Name: "Flag", FilePath: "flow.go", LineStart: 999},
		{ID: "symbol:go:example.com/flow:Current", Kind: domain.KindObject, Name: "Current", FilePath: "flow.go", LineStart: 999},
		{ID: "symbol:go:example.com/flow:Plain", Kind: domain.KindFunction, Name: "Plain", FilePath: "plain.go", LineStart: 999},
	}
	if _, err := r.SaveBatchStats(nodes, nil, nil); err != nil {
		t.Fatal(err)
	}
	return New(r), dir
}

func TestSymbolSourceImports(t *testing.T) {
	a, dir := seedSymbolSourceImportsRepo(t)
	want := []SymbolSourceImport{
		{Path: "fmt", Alias: "output"},
		{Path: "context"},
		{Path: "math", Alias: "."},
		{Path: "net/http/pprof", Alias: "_"},
	}
	for _, id := range []string{
		"symbol:go:example.com/flow:Entry",
		"symbol:go:example.com/flow:Value",
		"symbol:go:example.com/flow:Flag",
		"symbol:go:example.com/flow:Current",
	} {
		got, err := a.SymbolSource(SymbolSourceRequest{RepoAbs: dir, SymbolID: id})
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if !reflect.DeepEqual(got.Imports, want) {
			t.Errorf("%s imports = %+v，期望 %+v", id, got.Imports, want)
		}
	}
	// 无导入文件省略 imports（不伪造空数组/别名）。
	got, err := a.SymbolSource(SymbolSourceRequest{RepoAbs: dir, SymbolID: "symbol:go:example.com/flow:Plain"})
	if err != nil {
		t.Fatalf("Plain: %v", err)
	}
	if len(got.Imports) != 0 {
		t.Errorf("无导入文件不应输出 imports，got %+v", got.Imports)
	}
}

// TestSymbolSourceImportsDependency：依赖文件（离线 replace）同样携带 imports。
func TestSymbolSourceImportsDependency(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "app")
	dep := filepath.Join(root, "dep")
	for _, d := range []string{app, dep} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, content string) {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dep, "go.mod"), "module example.com/dep\n\ngo 1.26.2\n")
	write(filepath.Join(dep, "dep.go"), `package dep

import (
	str "strings"
	"context"
)

func Run(ctx context.Context) string { return str.ToUpper("x") }
`)
	write(filepath.Join(app, "go.mod"),
		"module example.com/app\n\ngo 1.26.2\n\nrequire example.com/dep v0.0.0\n\nreplace example.com/dep => ../dep\n")
	write(filepath.Join(app, "main.go"), "package app\n\nimport \"example.com/dep\"\n\nfunc Use() { dep.Run(nil) }\n")
	db, err := sqlite.Open(app)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a := New(sqlite.NewRepo(db))
	got, err := a.SymbolSource(SymbolSourceRequest{RepoAbs: app, SymbolID: "symbol:go:example.com/dep:Run"})
	if err != nil {
		t.Fatalf("dependency source: %v", err)
	}
	want := []SymbolSourceImport{{Path: "strings", Alias: "str"}, {Path: "context"}}
	if !reflect.DeepEqual(got.Imports, want) {
		t.Errorf("依赖 imports = %+v，期望 %+v", got.Imports, want)
	}
	if got.Location != "dependency" || !filepath.IsAbs(got.File) {
		t.Errorf("依赖定位契约被破坏: location=%q file=%q", got.Location, got.File)
	}
}
