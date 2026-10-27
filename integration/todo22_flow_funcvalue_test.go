//go:build integration

package integration

// todo22 §2（第一增量）真实 CLI 回归：回调只在**实际调用它的位置**展开，
// 未证明的回调值不得伪造内部调用。
//
// 夹具 = todo22 §8 的 example.com/flow（Entry → Execute → tx.Run → 回调 →
// process(inner)）。本轮已实现字面量/IIFE/局部函数值/回调形参绑定；
// 装饰器（变参列表 + 工厂返回函数 + 折叠）还没做——所以循环里
// `process = wrapper(ctx, process)` 之后 process 只能是**未消歧的多候选**
// （"改写后的未知值" 与 "循环未执行的旧值"），展开必须整体标 ambiguous，
// 不能把 Store.Insert 当成已证明的确定链路。

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// flowCLI 构建本轮二进制并在 §8 夹具上 init；返回 (bin, fixture)。
// 注意：这里必须用**外部进程**而不是 runCLIOut（后者把 os.Stdout 换成管道，
// 树大到超过管道缓冲时 json.Encoder 会写阻塞，测试表现成"分析卡住"）。
func flowCLI(t *testing.T) (string, string) {
	t.Helper()
	wd, _ := os.Getwd()
	repoRoot := filepath.Dir(wd)
	bin := filepath.Join(t.TempDir(), "codeintel")
	build := exec.Command("go", "build", "-o", bin, "./cmd/codeintel")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build codeintel: %v\n%s", err, out)
	}
	fixture := writeFlowFixture(t)
	if code, out := flowRun(t, bin, "init", "--repo", fixture, "--progress", "none"); code != 0 {
		t.Fatalf("init exit=%d\n%s", code, out)
	}
	return bin, fixture
}

func flowRun(t *testing.T, bin string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run %v: %v", args, err)
	}
	return code, stdout.String() + stderr.String()
}

func TestTodo22FlowFuncValueCallback(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	bin, fixture := flowCLI(t)
	code, out := flowRun(t, bin, "query", "sequence", "symbol:go:example.com/flow:Entry",
		"--repo", fixture, "--code", "--depth", "8", "--json")
	if code != 0 {
		t.Fatalf("sequence exit=%d\n%s", code, out)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("JSON 不可解码: %v\n%s", err, out)
	}

	// ① tx.Run 被展开；tx.Run 内部的 f(ctx) 是**回调绑定**证明的执行
	runs := []map[string]any{}
	seqJSONNodes(root, "tx.Run", &runs)
	if len(runs) != 1 {
		t.Fatalf("应展开 1 个 tx.Run 节点，got %d\n%s", len(runs), out)
	}
	// 只取 tx.Run 的**直接子节点**里的 f（更深层的 f 是回调链内部对同一
	// 回调变量的再次调用，不是"回调在这里被执行"的位置）。
	callbacks := []map[string]any{}
	if kids, ok := runs[0]["Nodes"].([]any); ok {
		for _, k := range kids {
			if km, ok := k.(map[string]any); ok && km["Kind"] == "call" && km["Label"] == "f" {
				callbacks = append(callbacks, km)
			}
		}
	}
	if len(callbacks) != 1 {
		t.Fatalf("tx.Run 的直接子节点应恰有 1 个回调执行节点 f，got %d\n%s", len(callbacks), out)
	}
	ev, _ := callbacks[0]["evidence"].(map[string]any)
	if ev == nil || ev["source"] != "closure" {
		t.Errorf("回调节点证据应来自 closure，got %+v", ev)
	}
	if ev != nil {
		if reason, _ := ev["reason"].(string); !strings.Contains(reason, "callback-binding") {
			t.Errorf("回调证据 reason 应说明绑定依据，got %q", reason)
		}
	}

	// ② 回调内部的 process(inner)：装饰器链未证明 → unknown，不展开内部执行
	procs := []map[string]any{}
	seqJSONNodes(callbacks[0], "process", &procs)
	if len(procs) != 1 {
		t.Fatalf("回调内应有 1 个 process 调用节点，got %d\n%s", len(procs), out)
	}
	pev, _ := procs[0]["evidence"].(map[string]any)
	if pev == nil || pev["ambiguous"] != true {
		t.Errorf("装饰器链未证明时 process 调用必须标 ambiguous（不能当确定结论），got %+v", pev)
	}
	if cands, _ := pev["candidates"].([]any); len(cands) < 2 {
		t.Errorf("多候选应列出全部可能值（旧值与改写后的未知值），got %+v", pev)
	}
	// ③ 若某个候选里出现 store.Insert，它必须挂在 ambiguous 节点之下
	// ——是"分别分析的候选"，不是已证明的确定链路（变参绑定/工厂折叠
	// 完成后 §8 的期望表才要求它成为确定链路）。
	// ④ 折叠后的两条路径必须分别落在各自的候选下（不串行、不任选）：
	//   - 候选 A（bypass：Second 直接返回原回调）→ BeforeFirst → Insert → AfterFirst
	//   - 候选 B（非 bypass：Second 返回包装闭包）→ BeforeSecond → BeforeFirst
	//     → Insert → AfterFirst → AfterSecond
	cands, _ := procs[0]["Nodes"].([]any)
	if len(cands) != 2 {
		t.Fatalf("装饰器折叠应产生 2 个候选（bypass / 非 bypass），got %d\n%s", len(cands), out)
	}
	var withSecond, withoutSecond int
	for _, c := range cands {
		cm, _ := c.(map[string]any)
		if cm == nil {
			t.Fatalf("候选节点不是对象: %+v", c)
		}
		beforeFirst := []map[string]any{}
		afterFirst := []map[string]any{}
		inserts := []map[string]any{}
		beforeSecond := []map[string]any{}
		afterSecond := []map[string]any{}
		seqJSONNodes(cm, "BeforeFirst", &beforeFirst)
		seqJSONNodes(cm, "AfterFirst", &afterFirst)
		seqJSONNodes(cm, "store.Insert", &inserts)
		seqJSONNodes(cm, "BeforeSecond", &beforeSecond)
		seqJSONNodes(cm, "AfterSecond", &afterSecond)
		if len(beforeFirst) != 1 || len(afterFirst) != 1 || len(inserts) != 1 {
			t.Errorf("候选必须包含 BeforeFirst→…→store.Insert→…→AfterFirst 各一次（before=%d insert=%d after=%d）",
				len(beforeFirst), len(inserts), len(afterFirst))
		}
		if len(inserts) == 1 {
			ev, _ := inserts[0]["evidence"].(map[string]any)
			if ev == nil || ev["symbol_id"] != "symbol:go:example.com/flow:(Store).Insert" {
				t.Errorf("Insert 证据身份错误: %+v", ev)
			}
		}
		if len(beforeSecond) > 0 {
			withSecond++
			if len(afterSecond) != 1 {
				t.Errorf("非 bypass 路径应含 AfterSecond 一次，got %d", len(afterSecond))
			}
		} else {
			withoutSecond++
		}
	}
	if withSecond != 1 || withoutSecond != 1 {
		t.Errorf("应恰好一条 bypass（无 Second 前后）与一条非 bypass 路径，got with=%d without=%d",
			withSecond, withoutSecond)
	}
}

// TestTodo22FlowSceneChecks：场景裁剪下的函数列表长度（Source=7 → 4 项；
// Source=19 → 7 项），每项恰好执行一次。
func TestTodo22FlowSceneChecks(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	bin, fixture := flowCLI(t)
	counts := func(scene string) map[string]int {
		t.Helper()
		code, out := flowRun(t, bin, "query", "sequence", "symbol:go:example.com/flow:Entry",
			"--repo", fixture, "--code", "--depth", "8", "--scene", scene, "--json")
		if code != 0 {
			t.Fatalf("scene=%s exit=%d\n%s", scene, code, out)
		}
		var root map[string]any
		if err := json.Unmarshal([]byte(out), &root); err != nil {
			t.Fatalf("scene=%s JSON: %v", scene, err)
		}
		got := map[string]int{}
		// 调用节点标签是 "check"（源码调用文本），执行的是哪个 Check 由
		// evidence.symbol_id 决定——按符号身份计数，不按标签。
		for i := 1; i <= 7; i++ {
			name := "Check" + string(rune('0'+i))
			nodes := []map[string]any{}
			walkSeqEvidenceSym(root, "symbol:go:example.com/flow:"+name, &nodes)
			got[name] = len(nodes)
		}
		return got
	}
	a := counts("Source=7")
	for i := 1; i <= 7; i++ {
		name := "Check" + string(rune('0'+i))
		want := 0
		if i <= 4 {
			want = 1
		}
		if a[name] != want {
			t.Errorf("Source=7 的 %s 执行次数=%d，期望 %d", name, a[name], want)
		}
	}
	b := counts("Source=19")
	for i := 1; i <= 7; i++ {
		name := "Check" + string(rune('0'+i))
		if b[name] != 1 {
			t.Errorf("Source=19 的 %s 执行次数=%d，期望 1", name, b[name])
		}
	}
}

// walkSeqEvidenceSym 收集 evidence.symbol_id 命中的调用节点（含嵌套）。
func walkSeqEvidenceSym(n map[string]any, symID string, out *[]map[string]any) {
	if n == nil {
		return
	}
	if ev, _ := n["evidence"].(map[string]any); ev != nil && ev["symbol_id"] == symID {
		*out = append(*out, n)
	}
	for _, key := range []string{"Nodes", "Else"} {
		cs, _ := n[key].([]any)
		for _, c := range cs {
			if m, ok := c.(map[string]any); ok {
				walkSeqEvidenceSym(m, symID, out)
			}
		}
	}
}
