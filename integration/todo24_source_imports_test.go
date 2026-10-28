//go:build integration

package integration

// todo24 §11.4：`query symbol --include-source --json` 的 source.imports 契约
// （真实 init → CLI；canonical ID 入口与名称入口共用同一提取逻辑）。

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

type sourceImportsOut struct {
	Source *struct {
		SymbolID string `json:"symbol_id"`
		File     string `json:"file"`
		Imports  []struct {
			Path  string `json:"path"`
			Alias string `json:"alias"`
		} `json:"imports"`
	} `json:"source"`
}

func TestSymbolSourceImportsRealCLI(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/flow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), `package flow

import (
	output "fmt"
	"context"
	. "math"
	_ "net/http/pprof"
)

type Value struct{ Ctx context.Context }

const Flag = Pi

var Current = Flag

func Entry() { output.Println(Flag) }
`)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	check := func(t *testing.T, args ...string) {
		t.Helper()
		code, stdout, stderr := runCLIOutErr(t, args...)
		if code != 0 {
			t.Fatalf("查询失败 exit=%d stderr=%q", code, stderr)
		}
		var out sourceImportsOut
		if err := json.Unmarshal([]byte(stdout), &out); err != nil {
			t.Fatalf("解析 JSON 失败: %v\n%s", err, stdout)
		}
		if out.Source == nil {
			t.Fatalf("缺少 source: %s", stdout)
		}
		want := []struct{ path, alias string }{
			{"fmt", "output"}, {"context", ""}, {"math", "."}, {"net/http/pprof", "_"},
		}
		if len(out.Source.Imports) != len(want) {
			t.Fatalf("imports 条数 = %d，期望 %d：%s", len(out.Source.Imports), len(want), stdout)
		}
		for i, w := range want {
			got := out.Source.Imports[i]
			if got.Path != w.path || got.Alias != w.alias {
				t.Errorf("imports[%d] = %+v，期望 path=%q alias=%q", i, got, w.path, w.alias)
			}
		}
	}
	// canonical ID 入口
	check(t, "query", "symbol", "symbol:go:example.com/flow:Entry",
		"--include-source", "--json", "--repo", dir)
	// 名称入口（旧 SymbolID 入口：先解析再取源码）
	check(t, "query", "symbol", "Entry", "--include-source", "--json", "--repo", dir)
	// 无 --include-source 时不附带源码上下文
	code, stdout, _ := runCLIOutErr(t, "query", "symbol", "Entry", "--json", "--repo", dir)
	if code != 0 {
		t.Fatalf("无 --include-source 查询失败 exit=%d", code)
	}
	if json.Valid([]byte(stdout)) {
		var out map[string]any
		if err := json.Unmarshal([]byte(stdout), &out); err == nil {
			if _, ok := out["source"]; ok {
				t.Errorf("未请求 --include-source 时不应附带 source: %s", stdout)
			}
		}
	}
}
