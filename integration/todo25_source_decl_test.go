//go:build integration

package integration

// todo25 §6：alias/const/var 的准确声明源码（真实 init → CLI）。

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
)

const todo25SourceFlow = `package src

import "context"

type Handler func(context.Context) error
type Alias = Handler

const (
	A = iota
	B
)

var (
	Left, Right = 1, 2
)
`

func TestTodo25SourceDeclAliasConstVar(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/src\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "src.go"), todo25SourceFlow)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "init")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit=%d", code)
	}
	cases := []struct {
		id    string
		texts []string
	}{
		{"symbol:go:example.com/src:Alias", []string{"type Alias = Handler"}},
		{"symbol:go:example.com/src:B", []string{"const (", "A = iota", "B"}},
		{"symbol:go:example.com/src:Right", []string{"var (", "Left, Right = 1, 2"}},
	}
	for _, tc := range cases {
		code, out := runCLIOut(t, "query", "symbol", tc.id, "--repo", dir, "--include-source", "--json")
		if code != 0 {
			t.Fatalf("%s exit=%d: %s", tc.id, code, out)
		}
		var got action.SymbolSource
		var wrapper struct {
			Source *action.SymbolSource `json:"source"`
		}
		if err := json.Unmarshal([]byte(out), &wrapper); err != nil || wrapper.Source == nil {
			t.Fatalf("%s source JSON: %v\n%s", tc.id, err, out)
		}
		got = *wrapper.Source
		for _, want := range tc.texts {
			if !strings.Contains(got.Text, want) {
				t.Errorf("%s 正文缺 %q:\n%s", tc.id, want, got.Text)
			}
		}
		if got.SelectionLineStart == 0 || got.SelectionLineEnd == 0 {
			t.Errorf("%s 缺 selection_line_*: %+v", tc.id, got)
		}
		if got.Location != "repository" && got.Location != "" {
			t.Errorf("%s location=%q", tc.id, got.Location)
		}
	}
}
