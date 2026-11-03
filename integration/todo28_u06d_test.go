//go:build integration

package integration

// todo28 U06d：已索引 vendor 源码的包身份校验（真实源码 → init → CLI）。
//
// vendor 依赖的 SymbolSource 必须按 vendor 边界 + modules.txt + go list 选中
// 文件校验（不能用"主 module 路径 + vendor 相对目录"）。

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func u06dFixture(t *testing.T) (dir, root string) {
	t.Helper()
	root = t.TempDir()
	writeFile(t, filepath.Join(root, "dep", "go.mod"), "module example.com/vendorlib\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(root, "dep", "dep.go"), `package vendorlib
type Store struct{}
func(*Store)Run(){}
`)
	writeFile(t, filepath.Join(root, "app", "go.mod"), `module example.com/vendorapp

go 1.26.2
require example.com/vendorlib v0.0.0
replace example.com/vendorlib => ../dep
`)
	writeFile(t, filepath.Join(root, "app", "app.go"), `package vendorapp
import "example.com/vendorlib"
func Entry(){(&vendorlib.Store{}).Run()}
`)
	dir = filepath.Join(root, "app")
	cmd := exec.Command("go", "mod", "vendor")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOPROXY=off", "GOSUMDB=off", "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go mod vendor: %v\n%s", err, out)
	}
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir, root
}

// TestU06dVendorSource vendor 依赖源码按准确包身份输出。
func TestU06dVendorSource(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir, _ := u06dFixture(t)
	code, out := runCLIOut(t, "query", "symbol", "symbol:go:example.com/vendorlib:(Store).Run",
		"--repo", dir, "--include-source", "--json")
	if code != 0 {
		t.Fatalf("include-source exit=%d\n%s", code, out)
	}
	for _, want := range []string{
		`"package":"example.com/vendorlib"`,
		`"file":"vendor/example.com/vendorlib/dep.go"`,
		`"location":"repository"`,
	} {
		if !strings.Contains(strings.ReplaceAll(out, " ", ""), want) {
			t.Fatalf("缺少 %s:\n%s", want, out)
		}
	}
	// 伪身份的逐项拒绝见 action_symbol_source_vendor_test.go（直接构造 reader
	// 节点，避免查询层名称回退掩盖身份校验）。
}
