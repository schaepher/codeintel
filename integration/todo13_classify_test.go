//go:build integration

package integration

// todo13-13 A1/A3：真实索引 → CLI 的调用分类与构建降级范围。
//   - strings.TrimSpace → external_target（不笼统 index_gap）
//   - 接口无实现 → unknown_implementation（保留接口身份/位置）
//   - 叶子 → no_calls；bad 包失败范围随 good 查询携带

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTodo13CallClassificationRealIndex(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/app\n\ngo 1.21\n")
	write("bad/bad.go", `package bad

var Broken int = "not-an-int"
`)
	write("good/good.go", `package good

import "strings"

type Runner interface{ Run() }

func Leaf() {}

func External() string { return strings.TrimSpace(" x ") }

func NoImpl(r Runner) { r.Run() }

func Caller(r Runner) {
	Leaf()
	External()
	NoImpl(r)
}
`)
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/app/good:Caller",
		"--repo", dir, "--code", "--depth", "3", "--json")
	if code != 0 {
		t.Fatalf("sequence exit = %d; %s", code, out)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("sequence JSON: %v\n%s", err, out)
	}
	type stopInfo struct{ code, detail string }
	stops := map[string]stopInfo{}
	walkSeqJSON(root, func(n map[string]any) {
		lbl, _ := n["Label"].(string)
		stop, _ := n["stop"].(map[string]any)
		if stop == nil {
			if ns, ok := n["Nodes"].([]any); ok && len(ns) == 1 {
				if child, ok := ns[0].(map[string]any); ok {
					stop, _ = child["stop"].(map[string]any)
				}
			}
		}
		if lbl != "" && stop != nil {
			c, _ := stop["code"].(string)
			d, _ := stop["detail"].(string)
			stops[lbl] = stopInfo{c, d}
		}
	})
	if s := stops["Leaf"]; s.code != "no_calls" {
		t.Errorf("叶子应 no_calls（不伪报索引缺失）: %+v", s)
	}
	if s := stops["strings.TrimSpace"]; s.code != "external_target" || !strings.Contains(s.detail, "strings") {
		t.Errorf("外部库目标应 external_target: %+v", s)
	}
	if s := stops["r.Run"]; s.code != "unknown_implementation" {
		t.Errorf("接口无实现应 unknown_implementation: %+v", s)
	}

	// 构建降级：good 查询携带 bad 包的具体失败位置 + 目标隔离判断
	meta, _ := root["meta"].(map[string]any)
	cov, _ := meta["coverage"].(map[string]any)
	if cov == nil {
		t.Fatalf("应携带 coverage: %+v", meta)
	}
	eps, _ := cov["error_packages"].([]any)
	if len(eps) == 0 {
		t.Fatalf("coverage 应带失败包明细: %+v", cov)
	}
	foundBad, foundPos := false, false
	for _, x := range eps {
		m, _ := x.(map[string]any)
		path, _ := m["path"].(string)
		if path == "example.com/app/bad" {
			foundBad = true
			if es, _ := m["errors"].([]any); len(es) > 0 {
				s, _ := es[0].(string)
				if strings.Contains(s, "bad/bad.go:3") {
					foundPos = true
				}
			}
		}
	}
	if !foundBad || !foundPos {
		t.Errorf("应记录失败包与错误位置: %+v", eps)
	}
	diags, _ := meta["diagnostics"].([]any)
	codes := map[string]string{}
	for _, d := range diags {
		m, _ := d.(map[string]any)
		c, _ := m["code"].(string)
		det, _ := m["detail"].(string)
		codes[c] = det
	}
	if _, ok := codes["degraded_package"]; !ok {
		t.Errorf("应有 degraded_package 诊断: %+v", codes)
	}
	if det, ok := codes["target_coverage"]; !ok || !strings.Contains(det, "不在失败包列表") {
		t.Errorf("目标包不在失败列表应说明隔离依据: %+v", codes)
	}
	if _, ok := codes["typecheck_errors"]; !ok {
		t.Errorf("应有 typecheck_errors 诊断: %+v", codes)
	}
	if _, ok := codes["sql_degraded"]; ok {
		t.Errorf("无 SQL 失败时不应报 sql_degraded: %+v", codes)
	}
	// 文本可读出分类
	code, text := runCLIOut(t, "query", "sequence", "symbol:go:example.com/app/good:Caller",
		"--repo", dir, "--code", "--depth", "3")
	if code != 0 || !strings.Contains(text, "external_target") ||
		!strings.Contains(text, "unknown_implementation") {
		t.Errorf("文本应能读出分类: code=%d\n%s", code, text)
	}
}
