package action

// T13：整组声明正文 + selection + package 上下文；SymbolID/Target 双请求兼容。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

const t13Src = `package m

import (
	"time"
	alias "strings"
)

type (
	Alias = Right
	Right struct{ At time.Time }
)

const (
	A = 1
	B = A + 1
)

var (
	X, Y = 1, 2
)

type Box[T any] struct{ V T }

func (*Box[T]) Work() {}
`

func seedT13(t *testing.T) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.26.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "flow.go"), []byte(t13Src), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return New(sqlite.NewRepo(db)), dir
}

func TestSymbolSourceGroupDeclarations(t *testing.T) {
	a, dir := seedT13(t)
	cases := []struct {
		name     string
		id       string
		kind     string
		wantHead string
		sel      string
	}{
		{"type group", "symbol:go:example.com/m:Alias", "type", "type (", "Alias = Right"},
		{"struct in group", "symbol:go:example.com/m:Right", "struct", "type (", "Right struct{ At time.Time }"},
		{"const group", "symbol:go:example.com/m:B", "const", "const (", "B = A + 1"},
		{"var group", "symbol:go:example.com/m:X", "var", "var (", "X, Y = 1, 2"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := a.SymbolSource(SymbolSourceRequest{RepoAbs: dir, SymbolID: c.id})
			if err != nil {
				t.Fatalf("SymbolSource: %v", err)
			}
			if got.Kind != c.kind {
				t.Errorf("kind = %q, want %q", got.Kind, c.kind)
			}
			if !strings.HasPrefix(got.Text, c.wantHead) {
				t.Errorf("text 应以 %q 开头（整组声明），实为 %q", c.wantHead, got.Text)
			}
			if !strings.Contains(got.Text, c.sel) {
				t.Errorf("整组正文应含选中项 %q: %q", c.sel, got.Text)
			}
			selStart := lineOfSrc(t13Src, c.sel)
			if got.SelectionLineStart != selStart || got.SelectionLineEnd != selStart {
				t.Errorf("selection = %d-%d, want %d", got.SelectionLineStart, got.SelectionLineEnd, selStart)
			}
			if got.LineStart >= got.SelectionLineStart {
				t.Errorf("line_start 应是整组起始（%d < selection %d）", got.LineStart, got.SelectionLineStart)
			}
			if got.Package != "example.com/m" {
				t.Errorf("package = %q, want example.com/m", got.Package)
			}
			if len(got.Imports) != 2 || got.Imports[0].Path != "time" || got.Imports[1].Alias != "alias" {
				t.Errorf("imports = %+v", got.Imports)
			}
		})
	}
}

// 泛型接收者方法：正文与位置来自当前 AST。
func TestSymbolSourceGenericReceiver(t *testing.T) {
	a, dir := seedT13(t)
	got, err := a.SymbolSource(SymbolSourceRequest{RepoAbs: dir,
		SymbolID: "symbol:go:example.com/m:(Box).Work"})
	if err != nil {
		t.Fatalf("SymbolSource: %v", err)
	}
	if !strings.HasPrefix(got.Text, "func (*Box[T]) Work()") {
		t.Errorf("text = %q", got.Text)
	}
	if got.Kind != "method" {
		t.Errorf("kind = %q", got.Kind)
	}
}

// SymbolID / Target 双入口兼容：只给 Target 也行；同时给出且不一致 → 失败。
func TestSymbolSourceRequestCompatibility(t *testing.T) {
	a, dir := seedT13(t)
	id := "symbol:go:example.com/m:Alias"
	got, err := a.SymbolSource(SymbolSourceRequest{RepoAbs: dir, Target: id})
	if err != nil || got.SymbolID != id {
		t.Fatalf("Target 入口应可用: %v %+v", err, got)
	}
	if _, err := a.SymbolSource(SymbolSourceRequest{RepoAbs: dir, SymbolID: id, Target: id}); err != nil {
		t.Fatalf("两者相同应通过: %v", err)
	}
	if _, err := a.SymbolSource(SymbolSourceRequest{RepoAbs: dir, SymbolID: id,
		Target: "symbol:go:example.com/m:Right"}); err == nil {
		t.Fatal("SymbolID 与 Target 不一致必须报错")
	}
	if _, err := a.SymbolSource(SymbolSourceRequest{RepoAbs: dir}); err == nil {
		t.Fatal("两个都缺必须报错")
	}
}
