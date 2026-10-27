//go:build integration

package integration

// todo22 §9.3：同一入口的 JSON 与 Mermaid 必须表达**同一分析树**——逐项核对
// 调用顺序（箭头顺序）与条件结构（alt/loop），不是"图能渲染"就算通过。
//
// 夹具 = todo22 §8 的 example.com/flow（含两层装饰器 + 事务回调 + 场景"检查"）。

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// seqJSONCallOrder JSON 树的先序调用标签（与渲染顺序一致）。
func seqJSONCallOrder(n map[string]any, out *[]string) {
	if n == nil {
		return
	}
	if n["Kind"] == "call" {
		if label, ok := n["Label"].(string); ok && label != "" {
			*out = append(*out, label)
		}
	}
	for _, key := range []string{"Nodes", "Else"} {
		cs, _ := n[key].([]any)
		for _, c := range cs {
			if m, ok := c.(map[string]any); ok {
				seqJSONCallOrder(m, out)
			}
		}
	}
}

var mermaidArrow = regexp.MustCompile(`^\s*P\d+->>P\d+:\s*(.+)$`)

// mermaidCallOrder Mermaid 的调用箭头标签顺序（去掉 `<br/>签名` 与槽位后缀）。
func mermaidCallOrder(src string) []string {
	var out []string
	for _, line := range strings.Split(src, "\n") {
		m := mermaidArrow.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		label := strings.TrimSpace(m[1])
		// 渲染约定：未消歧调用加 "? " 前缀（分析树里由 evidence.ambiguous 表达）
		label = strings.TrimPrefix(label, "? ")
		if i := strings.Index(label, "<br/>"); i >= 0 {
			label = label[:i]
		}
		if i := strings.Index(label, " [slots:"); i >= 0 {
			label = label[:i]
		}
		out = append(out, strings.TrimSpace(label))
	}
	return out
}

func TestTodo22JSONMermaidConsistency(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	bin, fixture := flowCLI(t)
	const target = "symbol:go:example.com/flow:Entry"
	base := []string{"query", "sequence", target, "--repo", fixture, "--code", "--depth", "8"}

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
		t.Fatalf("JSON 不可解码: %v", err)
	}
	var want []string
	seqJSONCallOrder(root, &want)
	// 渲染约定：入口节点是参与者本身，不画自调用箭头——去掉 JSON 的首个入口
	if len(want) > 0 && root["Label"] == want[0] {
		want = want[1:]
	}
	got := mermaidCallOrder(mmd)
	if len(want) == 0 {
		t.Fatalf("JSON 无调用节点\n%s", jsonOut)
	}
	if len(want) != len(got) {
		t.Fatalf("调用顺序条数不一致: JSON=%d Mermaid=%d\nJSON=%v\nMermaid=%v",
			len(want), len(got), want, got)
	}
	for i := range want {
		if want[i] != got[i] {
			t.Errorf("第 %d 个调用顺序不一致: JSON=%q Mermaid=%q\nJSON=%v\nMermaid=%v",
				i, want[i], got[i], want, got)
			break
		}
	}

	// 条件结构：JSON 的 branch/loop 标签必须出现在 Mermaid 的 alt/loop 行里
	var branches []string
	var walk func(map[string]any)
	walk = func(n map[string]any) {
		if n == nil {
			return
		}
		if k, _ := n["Kind"].(string); k == "branch" || k == "loop" {
			// 只有带子步骤的结构才在图里出现；纯诊断的空循环（如未消歧的
			// range 候选）只体现在 JSON 的 stop 里，不要求画出来。
			if label, ok := n["Label"].(string); ok && label != "" {
				if cs, _ := n["Nodes"].([]any); len(cs) > 0 {
					branches = append(branches, label)
				}
			}
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
	for _, b := range branches {
		if !strings.Contains(mmd, b) {
			t.Errorf("Mermaid 缺少 JSON 里的条件/循环结构 %q", b)
		}
	}
	// 该流程的关键结构必须都在（防"两边都空"的假一致）
	for _, must := range []string{"alt ", "loop range wrappers"} {
		if !strings.Contains(mmd, must) {
			t.Errorf("Mermaid 缺少关键结构 %q", must)
		}
	}
}

// TestTodo22FlowSVG：含两层装饰器 + 外部事务回调的流程导出 SVG（矢量图出口），
// 检查渲染产物真的包含关键节点与条件结构（不是"exit 0 就算过"）。
// 说明：本夹具**没有** RPC/DI 节点（§4.1/§3.3 的夹具另建），此处只覆盖
// §9.3 要求的"实际生成 SVG + 解析产物内容"。
func TestTodo22FlowSVG(t *testing.T) {
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
	bin, fixture := flowCLI(t)
	out := filepath.Join(t.TempDir(), "flow.svg")
	code, log := flowRun(t, bin, "query", "sequence", "symbol:go:example.com/flow:Entry",
		"--repo", fixture, "--code", "--depth", "8", "--format", "svg", "--out", out)
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
	for _, want := range []string{"BeforeFirst", "BeforeSecond", "AfterSecond", "store.Insert"} {
		if !strings.Contains(svg, want) {
			t.Errorf("SVG 缺少关键节点 %q", want)
		}
	}
	if !strings.Contains(svg, "alt") && !strings.Contains(svg, "group") {
		t.Errorf("SVG 未见条件结构（alt/group）")
	}
	t.Logf("SVG: %d bytes", len(data))
}
