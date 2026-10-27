//go:build integration

package integration

// todo22 §9.3：流程矩阵的最终输出验收——
//   - 含 RPC（go-micro 服务端续接）、DI/AOP、两层装饰器、外部事务回调的流程：
//     同一入口的 JSON 与 Mermaid 必须是同一棵树（箭头顺序 + 条件结构）；
//   - 实际导出 SVG 并检查产物内容（渲染成功与链路完整分别验收）；
//   - `CODEINTEL_TODO22_DOCS_OUT` 指定目录时导出交付用产物（JSON/Mermaid/SVG/
//     文本），供 docs/todo22/flow-matrix.md 引用（可复现）。

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const flowMatrixRenderTarget = "symbol:go:example.com/flowmatrix:(App).FlowAppNewBankAccount"

// TestTodo22FlowMatrixJSONMermaid 同一入口 JSON ↔ Mermaid 调用顺序与结构一致。
func TestTodo22FlowMatrixJSONMermaid(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	bin, dir := flowMatrixCLI(t)
	base := []string{"query", "sequence", flowMatrixRenderTarget, "--repo", dir,
		"--code", "--depth", "24", "--scene", "Source=19"}
	code, jsonOut := flowRun(t, bin, append(append([]string{}, base...), "--json")...)
	if code != 0 {
		t.Fatalf("JSON exit=%d\n%s", code, jsonOut)
	}
	code, mmd := flowRun(t, bin, append(append([]string{}, base...), "--format", "mermaid")...)
	if code != 0 {
		t.Fatalf("mermaid exit=%d\n%s", code, mmd)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &root); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var want []string
	seqJSONCallOrder(root, &want)
	if len(want) > 0 && root["Label"] == want[0] {
		want = want[1:] // 入口自身不画自调用箭头
	}
	got := mermaidCallOrder(mmd)
	if len(want) != len(got) {
		t.Fatalf("调用顺序条数不一致: JSON=%d Mermaid=%d\nJSON=%v\nMermaid=%v", len(want), len(got), want, got)
	}
	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("第 %d 个调用顺序不一致: JSON=%q Mermaid=%q", i, want[i], got[i])
		}
	}
	// 链路完整性（与渲染分开验收）：RPC/DI/装饰器/事务/审核/存储都在同一棵树里
	for _, must := range []string{
		"symbol:go:example.com/flowmatrix:(AccountServer).Apply", // RPC 服务端
		"symbol:go:example.com/flowmatrix:(storeAOP).Insert",     // DI/AOP
		"symbol:go:example.com/flowmatrix:BeforeFirst",           // 装饰器
		"symbol:go:example.com/flowmatrix:BeforeSecond",
		"symbol:go:example.com/tx:Run", // 外部事务回调
		"symbol:go:example.com/flowmatrix:FinishReview",
		"symbol:go:example.com/flowmatrix:(Store).SaveAccount",
		"symbol:go:example.com/flowmatrix:(Store).UpdateEntityState",
	} {
		found := false
		var walk func(map[string]any)
		walk = func(n map[string]any) {
			if n == nil || found {
				return
			}
			if ev, _ := n["evidence"].(map[string]any); ev != nil && ev["symbol_id"] == must {
				found = true
				return
			}
			for _, key := range []string{"Nodes", "Else"} {
				cs, _ := n[key].([]any)
				for _, c := range cs {
					if m, ok := c.(map[string]any); ok {
						walk(m)
					}
				}
			}
		}
		walk(root)
		if !found {
			t.Errorf("JSON 链路缺少 %s", must)
		}
	}
	// 条件结构（人工审核分支）在两边都可见
	if !strings.Contains(mmd, "alt ") {
		t.Errorf("Mermaid 缺少条件结构 alt")
	}
	var hasBranch bool
	var walkB func(map[string]any)
	walkB = func(n map[string]any) {
		if hasBranch || n == nil {
			return
		}
		if k, _ := n["Kind"].(string); k == "branch" {
			if cs, _ := n["Nodes"].([]any); len(cs) > 0 {
				hasBranch = true
				return
			}
		}
		for _, key := range []string{"Nodes", "Else"} {
			cs, _ := n[key].([]any)
			for _, c := range cs {
				if m, ok := c.(map[string]any); ok {
					walkB(m)
				}
			}
		}
	}
	walkB(root)
	if !hasBranch {
		t.Errorf("JSON 应保留人工/自动审核分支")
	}
}

// TestTodo22FlowMatrixSVG 含 RPC/DI/两层装饰器/外部事务回调的流程导出 SVG。
func TestTodo22FlowMatrixSVG(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	jar := os.Getenv("PLANTUML_JAR")
	if jar == "" {
		jar = "/opt/plantuml/plantuml.jar"
	}
	if _, err := os.Stat(jar); err != nil {
		t.Skipf("plantuml.jar 不可用: %v", err)
	}
	if _, err := exec.LookPath("java"); err != nil {
		t.Skip("java 不在 PATH")
	}
	t.Setenv("PLANTUML_JAR", jar)
	bin, dir := flowMatrixCLI(t)
	out := filepath.Join(t.TempDir(), "flow.svg")
	code, log := flowRun(t, bin, "query", "sequence", flowMatrixRenderTarget,
		"--repo", dir, "--code", "--depth", "24", "--scene", "Source=19",
		"--format", "svg", "--out", out)
	if code != 0 {
		t.Fatalf("svg exit=%d\n%s", code, log)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("未生成 SVG: %v\n%s", err, log)
	}
	svg := string(data)
	if !strings.Contains(svg, "<svg") {
		t.Fatalf("产物不是 SVG:\n%.200s", svg)
	}
	for _, want := range []string{
		"BeforeFirst", "BeforeSecond", "AfterSecond", // 装饰器
		"a.Store.Insert",                          // DI/AOP 调用点
		"SyncRegistry",                            // RPC 服务端（自动步骤）
		"FinishDataReview", "FinishServiceReview", // 人工审核
		"SaveAccount", "UpdateEntityState", // 主数据/状态
	} {
		if !strings.Contains(svg, want) {
			t.Errorf("SVG 缺少 %q", want)
		}
	}
	if !strings.Contains(svg, "alt") && !strings.Contains(svg, "group") {
		t.Errorf("SVG 未见条件结构")
	}
	t.Logf("SVG: %d bytes", len(data))
}

// TestTodo22DocOutputs 交付产物导出（env 指定目录时执行；默认 skip）。
// 产物：flow.json / flow.mmd / flow.svg / flow.txt（入口 = FlowWebNewEntity）。
func TestTodo22DocOutputs(t *testing.T) {
	outDir := os.Getenv("CODEINTEL_TODO22_DOCS_OUT")
	if outDir == "" {
		t.Skip("CODEINTEL_TODO22_DOCS_OUT 未设置")
	}
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin, dir := flowMatrixCLI(t)
	target := "symbol:go:example.com/flowmatrix:(App).FlowAppNewBankAccount"
	base := []string{"query", "sequence", target, "--repo", dir,
		"--code", "--depth", "24", "--scene", "Source=19"}
	type outp struct {
		name string
		args []string
	}
	for _, o := range []outp{
		{"flow.json", append(append([]string{}, base...), "--json")},
		{"flow.mmd", append(append([]string{}, base...), "--format", "mermaid")},
		{"flow.txt", base},
	} {
		code, out := flowRun(t, bin, o.args...)
		if code != 0 {
			t.Fatalf("%s exit=%d\n%s", o.name, code, out)
		}
		if err := os.WriteFile(filepath.Join(outDir, o.name), []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	svg := filepath.Join(outDir, "flow.svg")
	code, out := flowRun(t, bin, append(base, "--format", "svg", "--out", svg)...)
	if code != 0 {
		t.Logf("SVG 未生成（plantuml/java 不可用？exit=%d）：%s", code, out)
	}
}
