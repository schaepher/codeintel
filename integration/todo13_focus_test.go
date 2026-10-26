//go:build integration

package integration

// todo13-9 A2：真实索引 → CLI（--focus return 保留别名写入）。
//   - AssignStatus(alias,"wanted") 与其父实例定义保留
//   - AssignStatus(second,"unrelated") 被排除
//   - 无焦点对照仍显示两次调用

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const todo13FocusAliasSrc = `package regression

type Handler struct{ NodeStatus string }

func AssignStatus(h *Handler, status string) {
	h.NodeStatus = status
}

func AliasPair() string {
	first, second := &Handler{}, &Handler{}
	alias := first
	AssignStatus(alias, "wanted")
	AssignStatus(second, "unrelated")
	return alias.NodeStatus
}
`

func TestTodo13FocusReturnAliasRealIndex(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/codeintel-regression\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "focus.go"), []byte(todo13FocusAliasSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	sym := "symbol:go:example.com/codeintel-regression:AliasPair"
	count := func(extra ...string) (int, int, string) {
		args := append([]string{"query", "sequence", sym, "--repo", dir,
			"--code", "--depth", "3", "--json"}, extra...)
		code, out := runCLIOut(t, args...)
		if code != 0 {
			t.Fatalf("sequence exit = %d; %s", code, out)
		}
		root := map[string]any{}
		if err := json.Unmarshal([]byte(out), &root); err != nil {
			t.Fatalf("JSON: %v\n%s", err, out)
		}
		calls, assigns := 0, 0
		walkSeqJSON(root, func(n map[string]any) {
			lbl, _ := n["Label"].(string)
			kind, _ := n["Kind"].(string)
			if kind == "call" && strings.HasSuffix(lbl, "AssignStatus") {
				calls++
			}
			if kind == "assign" && (strings.Contains(lbl, "alias := first") ||
				strings.Contains(lbl, "first, second") || strings.Contains(lbl, "NodeStatus = status")) {
				assigns++
			}
		})
		return calls, assigns, out
	}
	calls, assigns, out := count("--focus", "return")
	if calls != 1 {
		t.Errorf("return 焦点应只保留 wanted 调用，实为 %d:\n%s", calls, out)
	}
	if assigns < 3 {
		t.Errorf("应保留 first 实例/alias/被调写入，实为 %d:\n%s", assigns, out)
	}
	if !strings.Contains(out, "return") {
		t.Errorf("应保留 return 节点:\n%s", out)
	}
	plain, _, plainOut := count()
	if plain != 2 {
		t.Errorf("无焦点应看到两次 AssignStatus，实为 %d:\n%s", plain, plainOut)
	}
}
