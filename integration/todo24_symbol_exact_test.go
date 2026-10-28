//go:build integration

package integration

// todo24 §11.3：canonical ID 精确未命中 → 非零退出、stdout 为空、stderr 明确
// not-found；普通名称查询仍正常（真实 init → CLI 全管道）。

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSymbolCanonicalMissNoNameScanRealCLI(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/flow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), `package flow

func Present() {}
`)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}

	// 准确 ID 未命中：非零退出、stdout 为空、stderr 说明 not found。
	code, stdout, stderr := runCLIOutErr(t, "query", "symbol",
		"symbol:go:example.com/flow:Missing", "--json", "--repo", dir)
	if code == 0 {
		t.Errorf("准确 ID 未命中应非零退出，got exit=0")
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("失败时 stdout 必须为空，got %q", stdout)
	}
	if !strings.Contains(stderr, "not found") {
		t.Errorf("stderr 应明确 not-found，got %q", stderr)
	}

	// 普通名称查询不受影响。
	code, stdout, stderr = runCLIOutErr(t, "query", "symbol", "Present", "--json", "--repo", dir)
	if code != 0 {
		t.Fatalf("名称查询应成功: exit=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "example.com/flow:Present") {
		t.Errorf("名称查询结果不符: %q", stdout)
	}
}
