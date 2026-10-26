//go:build integration

package integration

// todo13-3 A3：真实源码 → 生产索引 → CLI。
//   - rewrite(&region) / 别名后 Route(region) 为 unknown，两分支保留
//   - meta.scene.partial=true + 原因指向实际调用位置
//   - 两层传参来源为正行号（无 def@L0）

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const todo13SceneRewrite = `package regression

func Audit() {}

func Notify() {}

func Route(region string) {
	if region == "CN" {
		Audit()
	} else {
		Notify()
	}
}

func Rewritten(rewrite func(*string)) {
	region := "CN"
	rewrite(&region)
	Route(region)
}

func Aliased(rewrite func(*string)) {
	region := "CN"
	p := &region
	rewrite(p)
	Route(region)
}

func TwoLayer() {
	region := "CN"
	Mid(region)
}

func Mid(region string) {
	Route(region)
}
`

func setupTodo13SceneRepo(t *testing.T) string {
	t.Helper()
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/codeintel-regression\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scenarios.go"), []byte(todo13SceneRewrite), 0o644); err != nil {
		t.Fatal(err)
	}
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

func TestTodo13SceneRewriteRealIndex(t *testing.T) {
	dir := setupTodo13SceneRepo(t)
	base := "symbol:go:example.com/codeintel-regression:"
	root := querySeqJSON(t, dir, base+"Rewritten", "--depth", "3", "--evidence")
	fit := scenarioFit(t, root)
	if fit["status"] != "unknown" {
		t.Errorf("rewrite(&region) 后应 unknown: %+v", fit)
	}
	if fit["expected"] == "CN" {
		t.Errorf("不得把旧 CN 当作已知实参: %+v", fit)
	}
	cause, _ := fit["cause"].(string)
	if !strings.Contains(cause, "call-may-write") {
		t.Errorf("原因应指向改写调用: %+v", fit)
	}
	if src, _ := fit["source"].(string); !strings.Contains(src, "@L") || strings.Contains(src, "@L0") {
		t.Errorf("来源应为真实文件正行号: %+v", fit)
	}
	meta, _ := root["meta"].(map[string]any)
	sc, _ := meta["scene"].(map[string]any)
	if sc == nil || sc["partial"] != true {
		t.Errorf("meta.scene.partial 应为 true: %+v", meta)
	}
	if causes, _ := sc["causes"].([]any); len(causes) == 0 {
		t.Errorf("partial 应带原因: %+v", sc)
	}
	// 两分支都保留
	labels := map[string]bool{}
	walkSeqJSON(root, func(n map[string]any) {
		if lbl, _ := n["Label"].(string); lbl == "Audit" || lbl == "Notify" {
			labels[lbl] = true
		}
	})
	if !labels["Audit"] || !labels["Notify"] {
		t.Errorf("unknown 时两分支都应保留: %+v", labels)
	}
	// 文本也说明 partial
	code, out := runCLIOut(t, "query", "sequence", base+"Rewritten", "--repo", dir,
		"--code", "--depth", "3", "--evidence")
	if code != 0 || !strings.Contains(out, "完整性: partial") {
		t.Errorf("文本应说明完整性 partial: code=%d\n%s", code, out)
	}

	// 别名同语义
	fit = scenarioFit(t, querySeqJSON(t, dir, base+"Aliased", "--depth", "3"))
	if fit["status"] != "unknown" {
		t.Errorf("别名传入未知调用后应 unknown: %+v", fit)
	}

	// 两层传参来源为正行号
	fit = scenarioFit(t, querySeqJSON(t, dir, base+"TwoLayer", "--depth", "3"))
	if fit["status"] != "taken" {
		t.Errorf("两层传参 CN 应 taken: %+v", fit)
	}
	if src, _ := fit["source"].(string); !strings.Contains(src, "@L") || strings.Contains(src, "@L0") {
		t.Errorf("两层来源须为正行号: %+v", fit)
	}
}

