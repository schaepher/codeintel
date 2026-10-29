//go:build integration

package integration

// todo27 T03：条件装饰器与跨帧捕获（真实 init → CLI）。
//
// 覆盖：条件返回原回调 / 包装闭包（已知实参按 guard 裁剪）、多层同名
// 包装（`Use(Use(Hit))` 不产生假 cycle）、调用时捕获已被改写 → 新来源、
// 未知改写不恢复旧值。

import (
	"path/filepath"
	"strings"
	"testing"
)

const t03Src = `package flow
func Hit(){}
func Miss(){}
func GuardedA(){}
func GuardedB(){}
func GuardedC(){}
func GuardedD(){}
func Execute(f func()){f()}
func Wrap(enable bool, f func()) func(){
 if enable { return func(){GuardedA();f();GuardedB()} }
 return f
}
func Wrap2(k bool, f func()) func(){
 if k { return func(){GuardedA();f();GuardedB()} }
 return func(){GuardedC();f();GuardedD()}
}
func Use(process func())func(){return func(){process()}}
func EntryCondFalse(){Execute(Wrap(false,Hit))}
func EntryCondTrue(){Execute(Wrap(true,Hit))}
func EntryNested(){Execute(Use(Use(Hit)))}
func EntryMultiGuard(){Execute(Wrap2(true,Hit))}
func EntryCapture(){f:=func(){Hit()};Execute(func(){f=func(){Miss()};Execute(f)})}
func EntryUnknownRewrite(){f:=func(){Hit()};unknown(&f);Execute(f)}
func unknown(p *func()){}
`

func t03Fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/flow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), t03Src)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

// t03Executed 确定执行（不在候选分支内）的符号集合。
func t03Executed(t *testing.T, dir, entry string, want ...string) map[string]int {
	t.Helper()
	root := querySeqJSON(t, dir, "symbol:go:example.com/flow:"+entry, "--depth", "12")
	counts := map[string]int{}
	for _, w := range want {
		counts["symbol:go:example.com/flow:"+w] = 0
	}
	var all []struct {
		n   map[string]any
		amb bool
	}
	t02Walk(root, false, &all)
	for _, it := range all {
		if it.amb {
			continue
		}
		ev, _ := it.n["evidence"].(map[string]any)
		if ev == nil {
			continue
		}
		id, _ := ev["symbol_id"].(string)
		if _, ok := counts[id]; ok {
			counts[id]++
		}
	}
	return counts
}

func t03Assert(t *testing.T, dir, entry string, want map[string]int) {
	t.Helper()
	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	got := t03Executed(t, dir, entry, keys...)
	for k, v := range want {
		if got["symbol:go:example.com/flow:"+k] != v {
			t.Errorf("%s：%s 执行 %d 次（期望 %d）", entry, k, got["symbol:go:example.com/flow:"+k], v)
		}
	}
}

// TestT03ConditionalDecorators：guard 决定的链条 + 多层包装 + 捕获改写。
func TestT03ConditionalDecorators(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t03Fixture(t)
	// 条件返回原回调：false 只执行 Hit，包装链（GuardedA/B）不可达。
	t03Assert(t, dir, "EntryCondFalse", map[string]int{"Hit": 1, "GuardedA": 0, "GuardedB": 0})
	// 条件返回包装闭包：true 只执行包装链。
	t03Assert(t, dir, "EntryCondTrue", map[string]int{"Hit": 1, "GuardedA": 1, "GuardedB": 1})
	// 多层同名 wrapper：不产生假 cycle，Hit 只执行一次。
	t03Assert(t, dir, "EntryNested", map[string]int{"Hit": 1})
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/flow:EntryNested",
		"--repo", dir, "--code", "--depth", "12", "--format", "mermaid")
	if code != 0 {
		t.Fatalf("query exit=%d\n%s", code, out)
	}
	if strings.Contains(out, "cycle") {
		t.Errorf("多层包装回调不应产生 cycle\n%s", out)
	}
	// 多层 guard：true 的链执行，另一条互斥链（C/D）不执行。
	t03Assert(t, dir, "EntryMultiGuard", map[string]int{"GuardedA": 1, "Hit": 1, "GuardedB": 1, "GuardedC": 0, "GuardedD": 0})
	// 捕获在调用时已被改写 → 用新来源（Miss），旧 Hit 不执行。
	t03Assert(t, dir, "EntryCapture", map[string]int{"Miss": 1, "Hit": 0})
	// 未知改写（取址传外部）→ 不恢复旧值，明确 unbound。
	t03Assert(t, dir, "EntryUnknownRewrite", map[string]int{"Hit": 0, "Miss": 0})
	code2, out2 := runCLIOut(t, "query", "sequence", "symbol:go:example.com/flow:EntryUnknownRewrite",
		"--repo", dir, "--code", "--depth", "12", "--format", "mermaid")
	if code2 != 0 {
		t.Fatalf("query exit=%d\n%s", code2, out2)
	}
	if !strings.Contains(out2, "func_value_unbound") && !strings.Contains(out2, "取址逃逸") {
		t.Errorf("未知改写应给出明确停止诊断\n%s", out2)
	}
}
