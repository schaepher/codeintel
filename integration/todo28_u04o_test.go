//go:build integration

package integration

// todo28 U04o：外部递归定位使用主仓模块图（真实源码 → init → CLI）。
//
// app 依赖 outer/inner（replace 到 ../outer / ../selected），outer 自己的
// go.mod 又把 inner replace 到 ../private——查询必须选中主仓的 selected。

import (
	"path/filepath"
	"strings"
	"testing"
)

func u04oFixture(t *testing.T) (dir string, root string) {
	t.Helper()
	root = t.TempDir()
	writeFile(t, filepath.Join(root, "app", "go.mod"), `module example.com/graphapp

go 1.26.2
require (
 example.com/outer v0.0.0
 example.com/inner v0.0.0
)
replace example.com/outer => ../outer
replace example.com/inner => ../selected
`)
	writeFile(t, filepath.Join(root, "app", "flow.go"), `package graphapp
import "example.com/outer"
func Entry(){outer.Run()}
`)
	writeFile(t, filepath.Join(root, "outer", "go.mod"), `module example.com/outer

go 1.26.2
require example.com/inner v0.0.0
replace example.com/inner => ../private
`)
	writeFile(t, filepath.Join(root, "outer", "flow.go"), `package outer
import "example.com/inner"
func Run(){inner.Run()}
`)
	writeFile(t, filepath.Join(root, "selected", "go.mod"), "module example.com/inner\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(root, "selected", "flow.go"), `package inner
func Run(){Selected()}
func Selected(){}
`)
	writeFile(t, filepath.Join(root, "private", "go.mod"), "module example.com/inner\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(root, "private", "flow.go"), `package inner
func Run(){Private()}
func Private(){}
`)
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

// TestU04oMainRepoModuleGraph 选中主仓实际模块（不是依赖自己的 replace）。
func TestU04oMainRepoModuleGraph(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir, _ := u04oFixture(t)
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/graphapp:Entry",
		"--repo", dir, "--code", "--depth", "12", "--continue-segments", "32", "--evidence", "--json")
	if code != 0 {
		t.Fatalf("query exit=%d\n%s", code, out)
	}
	if !strings.Contains(out, `"Label": "Selected"`) {
		t.Fatalf("应读取主仓选中的 inner（Selected）:\n%s", out)
	}
	if strings.Contains(out, `"Label": "Private"`) {
		t.Fatalf("不得读取依赖自己的 replace（Private）:\n%s", out)
	}
}
