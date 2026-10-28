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

// TestTodo25SourceMultiModuleIdentity：两个嵌套 module 都有 package pkg，声明
// 源码必须按各自 module 身份定到正确文件；用 A 的 import path 查 B 的符号
// 必须失败（不跨 module 借用同名声明）。
func TestTodo25SourceMultiModuleIdentity(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	root := t.TempDir()
	a := filepath.Join(root, "a")
	b := filepath.Join(root, "b")
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/root\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(root, "root.go"), "package root\n")
	writeFile(t, filepath.Join(a, "go.mod"), "module example.com/a\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(a, "pkg", "x.go"), "package pkg\n\nfunc OnlyA() {}\n")
	writeFile(t, filepath.Join(b, "go.mod"), "module example.com/b\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(b, "pkg", "x.go"), "package pkg\n\nfunc OnlyB() {}\n")
	gitRun(t, root, "init")
	gitRun(t, root, "add", "-A")
	gitRun(t, root, "commit", "-m", "init")
	clearIndex(t, root)
	if code := runCLI(t, "init", "--repo", root, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit=%d", code)
	}
	for _, tc := range []struct{ id, file, text string }{
		{"symbol:go:example.com/a/pkg:OnlyA", "a/pkg/x.go", "func OnlyA"},
		{"symbol:go:example.com/b/pkg:OnlyB", "b/pkg/x.go", "func OnlyB"},
	} {
		code, out := runCLIOut(t, "query", "symbol", tc.id, "--repo", root, "--include-source", "--json")
		if code != 0 {
			t.Fatalf("%s exit=%d: %s", tc.id, code, out)
		}
		var wrapper struct {
			Source *action.SymbolSource `json:"source"`
		}
		if err := json.Unmarshal([]byte(out), &wrapper); err != nil || wrapper.Source == nil {
			t.Fatalf("%s source JSON: %v\n%s", tc.id, err, out)
		}
		if wrapper.Source.File != tc.file || !strings.Contains(wrapper.Source.Text, tc.text) {
			t.Errorf("%s 定位错误: file=%q text=%q", tc.id, wrapper.Source.File, wrapper.Source.Text)
		}
	}
	// 用 A 的包身份查只存在于 B 的符号：不得跨 module 借用。
	code, out, errOut := runCLIOutErr(t, "query", "symbol", "symbol:go:example.com/a/pkg:OnlyB",
		"--repo", root, "--include-source", "--json")
	if code == 0 || strings.TrimSpace(out) != "" {
		t.Errorf("跨 module 同名包查不到声明应失败且 stdout 为空: exit=%d out=%q", code, out)
	}
	if !strings.Contains(errOut, "找不到") && !strings.Contains(errOut, "error") {
		t.Errorf("失败原因应写 stderr: %q", errOut)
	}
}

// TestTodo25SourceStdlibFailure：标准库符号不伪造源码（非零退出 + stdout 空）。
func TestTodo25SourceStdlibFailure(t *testing.T) {
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
	code, out, errOut := runCLIOutErr(t, "query", "symbol", "symbol:go:fmt:Println",
		"--repo", dir, "--include-source", "--json")
	if code == 0 || strings.TrimSpace(out) != "" {
		t.Errorf("标准库源码应明确失败且 stdout 为空: exit=%d out=%q", code, out)
	}
	if !strings.Contains(errOut, "标准库") {
		t.Errorf("失败原因应说明标准库不支持: %q", errOut)
	}
}
