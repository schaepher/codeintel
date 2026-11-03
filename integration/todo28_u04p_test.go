//go:build integration

package integration

// todo28 U04p：续接外部包内准确声明的同包函数（真实源码 → init → CLI）。

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func u04pFixture(t *testing.T, afterExtra string) (dir, root string) {
	t.Helper()
	root = t.TempDir()
	writeFile(t, filepath.Join(root, "app", "go.mod"), `module example.com/localapp

go 1.26.2
require example.com/framework v0.0.0
replace example.com/framework => ../framework
`)
	writeFile(t, filepath.Join(root, "app", "flow.go"), `package localapp
import "example.com/framework"
func Entry(){framework.Run()}
func ShadowEntry(){framework.Shadow()}
func ParamEntry(){framework.Param(nil)}
func RepeatedEntry(){framework.Repeated()}
func WithEntry(){framework.With(func(){framework.Hit()})}
`)
	writeFile(t, filepath.Join(root, "framework", "go.mod"), "module example.com/framework\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(root, "framework", "flow.go"), `package framework
func Run(){Before();After()}
func Before(){}
func Hit(){}
func Shadow(){Before:=func(){Local()};Before()}
func Local(){}
func Param(Before func()){Before()}
func Recursive(){again()}
func Repeated(){Before();Before()}
func With(cb func()){Before();cb();After()}
`)
	writeFile(t, filepath.Join(root, "framework", "after.go"), "package framework\nfunc After(){}\nfunc again(){again()}\n")
	if afterExtra != "" {
		writeFile(t, filepath.Join(root, "framework", "ignored.go"), afterExtra)
	}
	dir = filepath.Join(root, "app")
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir, root
}

func u04pQuery(t *testing.T, dir, entry string) map[string]any {
	t.Helper()
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/localapp:"+entry,
		"--repo", dir, "--code", "--depth", "12", "--continue-segments", "16",
		"--evidence", "--json")
	if code != 0 {
		t.Fatalf("query %s exit=%d\n%s", entry, code, out)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("json: %v", err)
	}
	return root
}

// TestU04pSamePkgExternals 同包跨文件函数按准确 canonical ID 续接。
func TestU04pSamePkgExternals(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir, _ := u04pFixture(t, "")
	root := u04pQuery(t, dir, "Entry")
	found := map[string]bool{}
	var walk func(n map[string]any)
	walk = func(n map[string]any) {
		if label, _ := n["Label"].(string); label == "Before" || label == "After" {
			if ev, ok := n["evidence"].(map[string]any); ok {
				id, _ := ev["symbol_id"].(string)
				found[label] = strings.HasPrefix(id, "symbol:go:example.com/framework:")
			}
		}
		for _, key := range []string{"Nodes", "Else"} {
			if arr, ok := n[key].([]any); ok {
				for _, c := range arr {
					walk(c.(map[string]any))
				}
			}
		}
	}
	walk(root)
	if !found["Before"] || !found["After"] {
		t.Fatalf("Before/After 应解析到外部包准确符号: %+v", found)
	}
}

// TestU04pShadowAndRecursion 遮蔽/形参/递归/重复调用的边界。
func TestU04pShadowAndRecursion(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir, _ := u04pFixture(t, "")
	// Shadow：局部 Before 闭包调用，不得接到包级 Before。
	shadow := u04pQuery(t, dir, "ShadowEntry")
	var pkgBefore int
	var walk func(n map[string]any)
	walk = func(n map[string]any) {
		if ev, ok := n["evidence"].(map[string]any); ok {
			if id, _ := ev["symbol_id"].(string); id == "symbol:go:example.com/framework:Before" {
				pkgBefore++
			}
		}
		for _, key := range []string{"Nodes", "Else"} {
			if arr, ok := n[key].([]any); ok {
				for _, c := range arr {
					walk(c.(map[string]any))
				}
			}
		}
	}
	walk(shadow)
	if pkgBefore != 0 {
		t.Fatalf("局部遮蔽的 Before 不得接包级 Before（got %d）", pkgBefore)
	}
	// Repeated：两次调用各自展开（外部同包调用只建一个节点，但调用次数=2）。
	repeated := u04pQuery(t, dir, "RepeatedEntry")
	count := strings.Count(mustJSON(t, repeated), `"Label":"Before"`)
	if count != 2 {
		t.Fatalf("Repeated 的 Before 应出现 2 次，got %d", count)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// TestU04pIgnoredFileAndWith 构建标签排除的文件不供声明；With 回调顺序。
func TestU04pIgnoredFileAndWith(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	extra := "//go:build excluded\n\npackage framework\nfunc WrongCalled(){}\n"
	dir, _ := u04pFixture(t, extra)
	with := u04pQuery(t, dir, "WithEntry")
	js := mustJSON(t, with)
	if !strings.Contains(js, `"Label":"Before"`) || !strings.Contains(js, `"Label":"After"`) ||
		!strings.Contains(js, `"Label":"framework.Hit"`) {
		t.Fatalf("With 应包含 Before/回调内 Hit/After:\n%s", js)
	}
	if strings.Contains(js, "WrongCalled") {
		t.Fatalf("被构建标签排除的声明不得出现")
	}
}
