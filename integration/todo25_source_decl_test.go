//go:build integration

package integration

// todo25 §6：alias/const/var 的准确声明源码（真实 init → CLI）。

import (
	"encoding/json"
	"os"
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

// TestTodo25SourceDependencyOffline：离线 replace 的依赖声明源码 location/
// 绝对路径/不下载/不改 go.mod。
func TestTodo25SourceDependencyOffline(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	root := t.TempDir()
	tx := filepath.Join(root, "tx")
	src := filepath.Join(root, "src")
	writeFile(t, filepath.Join(tx, "go.mod"), "module example.com/tx\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(tx, "tx.go"), "package tx\n\nfunc Run() {}\n")
	writeFile(t, filepath.Join(src, "go.mod"),
		"module example.com/src\n\ngo 1.26.2\n\nrequire example.com/tx v0.0.0\n\nreplace example.com/tx => ../tx\n")
	writeFile(t, filepath.Join(src, "main.go"), "package main\n\nimport \"example.com/tx\"\n\nfunc main() { tx.Run() }\n")
	gitRun(t, src, "init")
	gitRun(t, src, "add", "-A")
	gitRun(t, src, "commit", "-m", "init")
	clearIndex(t, src)
	if code := runCLI(t, "init", "--repo", src, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit=%d", code)
	}
	before, _ := os.ReadFile(filepath.Join(src, "go.mod"))
	code, out := runCLIOut(t, "query", "symbol", "symbol:go:example.com/tx:Run",
		"--repo", src, "--include-source", "--json")
	if code != 0 {
		t.Fatalf("dependency source exit=%d: %s", code, out)
	}
	var wrapper struct {
		Source *action.SymbolSource `json:"source"`
	}
	if err := json.Unmarshal([]byte(out), &wrapper); err != nil || wrapper.Source == nil {
		t.Fatalf("source JSON: %v\n%s", err, out)
	}
	if wrapper.Source.Location != "dependency" {
		t.Errorf("location=%q; want dependency", wrapper.Source.Location)
	}
	if !filepath.IsAbs(wrapper.Source.File) {
		t.Errorf("依赖 file 应为绝对路径: %q", wrapper.Source.File)
	}
	if !strings.Contains(wrapper.Source.Text, "func Run()") {
		t.Errorf("依赖源码正文不符: %q", wrapper.Source.Text)
	}
	after, _ := os.ReadFile(filepath.Join(src, "go.mod"))
	if string(before) != string(after) {
		t.Errorf("查询依赖源码不得修改 go.mod")
	}
	if _, err := os.Stat(filepath.Join(src, "go.sum")); err == nil {
		t.Errorf("离线查询不得生成 go.sum")
	}
}

// TestTodo25SourceFailures：未知符号失败时非零退出、stdout 为空。
func TestTodo25SourceFailures(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/m\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "m.go"), "package m\n\nfunc Exists(){}\n")
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "init")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit=%d", code)
	}
	code, out, errOut := runCLIOutErr(t, "query", "symbol", "symbol:go:example.com/m:Missing",
		"--repo", dir, "--include-source", "--json")
	if code == 0 || strings.TrimSpace(out) != "" {
		t.Errorf("未知符号应失败且 stdout 为空: exit=%d out=%q", code, out)
	}
	if !strings.Contains(errOut, "error") {
		t.Errorf("失败原因应写 stderr: %q", errOut)
	}
}
