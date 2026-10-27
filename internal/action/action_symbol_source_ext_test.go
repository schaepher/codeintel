package action

// todo23 §6：SymbolSource 扩展——struct/interface/type alias/const/var、
// const/var 块全文 + selection 范围、无节点返回 index_status=not_indexed、
// 依赖虚拟函数按离线 go list 定位（location=dependency，绝对 file）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

const symbolSourceExtSrc = `package m

type Handler func()

type Alias = Handler

type Iface interface{ Do() }

type Box struct{ N int }

const (
	A = iota
	B
	NotIndexed = 3
)

var (
	Left, Right = 1, 2
)
`

func seedSymbolSourceExtRepo(t *testing.T) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(symbolSourceExtSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:Handler", Kind: domain.KindStruct, Name: "Handler", FilePath: "main.go", LineStart: 999},
		{ID: "symbol:go:example.com/m:Alias", Kind: domain.KindStruct, Name: "Alias", FilePath: "main.go", LineStart: 999},
		{ID: "symbol:go:example.com/m:Iface", Kind: domain.KindInterface, Name: "Iface", FilePath: "main.go", LineStart: 999},
		{ID: "symbol:go:example.com/m:Box", Kind: domain.KindStruct, Name: "Box", FilePath: "main.go", LineStart: 999},
		{ID: "symbol:go:example.com/m:B", Kind: domain.KindObject, Name: "B", FilePath: "main.go", LineStart: 999},
		{ID: "symbol:go:example.com/m:Right", Kind: domain.KindObject, Name: "Right", FilePath: "main.go", LineStart: 999},
	}
	if _, err := r.SaveBatchStats(nodes, nil, nil); err != nil {
		t.Fatal(err)
	}
	return New(r), dir
}

func lineOfSrc(src, prefix string) int {
	for i, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			return i + 1
		}
	}
	return 0
}

func TestSymbolSourceKindsAndSelection(t *testing.T) {
	a, dir := seedSymbolSourceExtRepo(t)
	cases := []struct {
		id      string
		kind    string
		wantSub string
		selLine int
	}{
		{"symbol:go:example.com/m:Alias", "type", "type Alias = Handler", lineOfSrc(symbolSourceExtSrc, "type Alias")},
		{"symbol:go:example.com/m:Iface", "interface", "type Iface interface{ Do() }", lineOfSrc(symbolSourceExtSrc, "type Iface")},
		{"symbol:go:example.com/m:Box", "struct", "type Box struct{ N int }", lineOfSrc(symbolSourceExtSrc, "type Box")},
		{"symbol:go:example.com/m:B", "const", "const (", lineOfSrc(symbolSourceExtSrc, "B")},
		{"symbol:go:example.com/m:Right", "var", "var (", lineOfSrc(symbolSourceExtSrc, "Left, Right")},
	}
	for _, c := range cases {
		got, err := a.SymbolSource(SymbolSourceRequest{RepoAbs: dir, SymbolID: c.id})
		if err != nil {
			t.Errorf("%s: %v", c.id, err)
			continue
		}
		if got.Kind != c.kind {
			t.Errorf("%s: kind = %q，期望 %q", c.id, got.Kind, c.kind)
		}
		if !strings.HasPrefix(got.Text, c.wantSub) {
			t.Errorf("%s: text 首行 %q，期望以 %q 开头", c.id, strings.SplitN(got.Text, "\n", 2)[0], c.wantSub)
		}
		if got.LineStart > c.selLine || got.LineEnd < c.selLine {
			t.Errorf("%s: 声明范围 %d-%d 应包含选中行 %d", c.id, got.LineStart, got.LineEnd, c.selLine)
		}
		if c.kind == "const" || c.kind == "var" {
			if got.SelectionLineStart != c.selLine || got.SelectionLineEnd != c.selLine {
				t.Errorf("%s: selection 行 %d-%d，期望 %d", c.id,
					got.SelectionLineStart, got.SelectionLineEnd, c.selLine)
			}
			if got.LineStart == got.LineEnd {
				t.Errorf("%s: const/var 块应输出整块（line_start<line_end），got %d-%d",
					c.id, got.LineStart, got.LineEnd)
			}
		}
		if got.Location != "repository" {
			t.Errorf("%s: location = %q，期望 repository", c.id, got.Location)
		}
	}
	// 无节点但 AST 有声明 → index_status=not_indexed，不写伪节点
	got, err := a.SymbolSource(SymbolSourceRequest{RepoAbs: dir, SymbolID: "symbol:go:example.com/m:NotIndexed"})
	if err != nil {
		t.Fatalf("not_indexed: %v", err)
	}
	if got.IndexStatus != "not_indexed" {
		t.Errorf("index_status = %q，期望 not_indexed", got.IndexStatus)
	}
	if got.Kind != "const" || !strings.Contains(got.Text, "NotIndexed") {
		t.Errorf("not_indexed 仍应返回 AST 声明: %+v", got)
	}
}

// TestSymbolSourceDependency：索引无文件位置的依赖函数 → 离线 go list 定位，
// location=dependency、绝对 file、不联网/不建索引。
func TestSymbolSourceDependency(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "app")
	dep := filepath.Join(root, "dep")
	for _, d := range []string{app, dep} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dep, "go.mod"), []byte("module example.com/dep\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dep, "dep.go"), []byte("package dep\n\nfunc Run() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "go.mod"),
		[]byte("module example.com/app\n\ngo 1.21\n\nrequire example.com/dep v0.0.0\n\nreplace example.com/dep => ../dep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "main.go"), []byte("package app\n\nimport \"example.com/dep\"\n\nfunc Use() { dep.Run() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(app)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a := New(sqlite.NewRepo(db))
	before, _ := os.ReadFile(filepath.Join(app, "go.mod"))
	got, err := a.SymbolSource(SymbolSourceRequest{RepoAbs: app, SymbolID: "symbol:go:example.com/dep:Run"})
	if err != nil {
		t.Fatalf("dependency source: %v", err)
	}
	if got.Location != "dependency" {
		t.Errorf("location = %q，期望 dependency", got.Location)
	}
	if !filepath.IsAbs(got.File) || !strings.HasSuffix(got.File, filepath.Join("dep", "dep.go")) {
		t.Errorf("依赖 file 应为绝对路径（dep.go），got %q", got.File)
	}
	if !strings.Contains(got.Text, "func Run()") {
		t.Errorf("text = %q", got.Text)
	}
	if got.IndexStatus != "not_indexed" {
		t.Errorf("index_status = %q", got.IndexStatus)
	}
	after, _ := os.ReadFile(filepath.Join(app, "go.mod"))
	if string(before) != string(after) {
		t.Errorf("go list 离线定位不得修改 go.mod")
	}
}
