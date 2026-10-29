//go:build integration

package integration

// todo27 T01：局部闭包的词法身份 / 到达定义（取址逃逸）/ 嵌套回调不产生
// 假 cycle，真正递归仍停止。
//
// 生产链路：源码 → init → CLI。断言按"确定执行"计数（候选不算），并核对
// 停止原因（func_value_unbound / cycle）。

import (
	"path/filepath"
	"strings"
	"testing"
)

const t01Src = `package flow
func Hit(){}
func Miss(){}
func Execute(f func()){f()}
func Retry(f func()){for i:=0;i<3;i++{f()}}
func Nested(){Execute(func(){Execute(func(){Hit()})})}
func Overwrite(){f:=func(){Miss()};f=func(){Hit()};Execute(f)}
func PtrEscape(){f:=func(){Hit()};p:=&f;*p=func(){Miss()};Execute(f)}
func Captured(){g:=func(){Hit()};Execute(func(){g=func(){Miss()};g()})}
func Unused(){_=func(){Hit()}}
func Retried(){Execute(func(){Retry(func(){Hit()})})}
func Cycle(){Cycle()}
`

func t01Fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/flow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), t01Src)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

func t01Seq(t *testing.T, dir, sym string) string {
	t.Helper()
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/flow:"+sym,
		"--repo", dir, "--code", "--depth", "12", "--format", "mermaid")
	if code != 0 {
		t.Fatalf("query %s exit=%d\n%s", sym, code, out)
	}
	return out
}

// t01Count 证据注释里某个符号作为调用目标的次数（确定执行口径：候选分支
// 的 `%% evidence` 行同样计数，故调用方需先排除 candidate 场景）。
func t01Count(out, symbol string) int {
	return strings.Count(out, "id=symbol:go:example.com/flow:"+symbol+" verdict=")
}

// TestT01ClosureIdentity：局部闭包的到达定义与嵌套回调身份。
func TestT01ClosureIdentity(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t01Fixture(t)
	// 嵌套实际回调：Hit 恰好一次，不出现假 cycle。
	nested := t01Seq(t, dir, "Nested")
	if got := t01Count(nested, "Hit"); got != 1 {
		t.Errorf("Nested 应执行 Hit 一次，got %d\n%s", got, nested)
	}
	if strings.Contains(nested, "cycle") {
		t.Errorf("嵌套回调不应产生 cycle\n%s", nested)
	}
	// 调用前覆盖：只用新绑定的 Hit，旧 Miss 不执行。
	over := t01Seq(t, dir, "Overwrite")
	if t01Count(over, "Hit") != 1 || t01Count(over, "Miss") != 0 {
		t.Errorf("Overwrite 应只用覆盖后的 Hit（Hit=1/Miss=0）\n%s", over)
	}
	// 闭包捕获的后续改写：执行新来源 Miss，旧 Hit 不执行。
	captured := t01Seq(t, dir, "Captured")
	if t01Count(captured, "Miss") != 1 || t01Count(captured, "Hit") != 0 {
		t.Errorf("Captured 应执行改写后的 Miss（Miss=1/Hit=0）\n%s", captured)
	}
	// 未执行字面量：不展开。
	unused := t01Seq(t, dir, "Unused")
	if t01Count(unused, "Hit") != 0 {
		t.Errorf("未执行的函数字面量不应展开\n%s", unused)
	}
	// retry 内的实际回调仍绑定到 Hit。
	retried := t01Seq(t, dir, "Retried")
	if t01Count(retried, "Hit") != 1 {
		t.Errorf("Retried 应执行 Hit 一次\n%s", retried)
	}
}

// TestT01AddrEscapeInvalidates：取址逃逸（`p := &f; *p = ...`）使旧回调
// 证明失效；真正递归仍停止（cycle）。
func TestT01AddrEscapeInvalidates(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t01Fixture(t)
	esc := t01Seq(t, dir, "PtrEscape")
	if t01Count(esc, "Hit") != 0 || t01Count(esc, "Miss") != 0 {
		t.Errorf("取址逃逸后不得沿用旧回调（也不得猜测新回调）\n%s", esc)
	}
	if !strings.Contains(esc, "func_value_unbound") && !strings.Contains(esc, "取址逃逸") {
		t.Errorf("取址逃逸应给出明确停止诊断\n%s", esc)
	}
	cyc := t01Seq(t, dir, "Cycle")
	if !strings.Contains(cyc, "cycle") {
		t.Errorf("真正递归应以 cycle 停止\n%s", cyc)
	}
}
