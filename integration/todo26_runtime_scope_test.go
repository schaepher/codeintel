//go:build integration

package integration

// todo26 U01/U02：运行时快照的重复 scope 拒绝与状态预算拒绝（真实 init→CLI）。

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/cli"
)

// writeTodo26Repo 写一个单文件模块 + 快照，git 初始化并全量 init。
func writeTodo26Repo(t *testing.T, src, snapshot string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/flow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), src)
	snap := filepath.Join(dir, "runtime.json")
	writeFile(t, snap, snapshot)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "init")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit=%d", code)
	}
	return dir, snap
}

// runCLIOutErrCtx 同 runCLIOutErr，但允许传入 ctx（取消回归用）。
func runCLIOutErrCtx(t *testing.T, ctx context.Context, args ...string) (int, string, string) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	ro, wo, _ := os.Pipe()
	re, we, _ := os.Pipe()
	os.Stdout, os.Stderr = wo, we
	var outBuf, errBuf strings.Builder
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(&outBuf, ro)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(&errBuf, re)
		done <- struct{}{}
	}()
	code := cli.Main(ctx, args)
	wo.Close()
	we.Close()
	os.Stdout, os.Stderr = oldOut, oldErr
	<-done
	<-done
	return code, outBuf.String(), errBuf.String()
}

const todo26U02Src = `package flow
func Hit(){}
func Miss(){}
func Entry(a,b,c,d,e,f,g bool){
	n:=0
	if a{n++};if b{n++};if c{n++};if d{n++};if e{n++};if f{n++};if g{n++}
	if n==0{Hit()}else{Miss()}
}
`

const todo26U01Snap = `{
  "version": 2,
  "sources": [{"id": "review", "kind": "database"}],
  "observations": [
    {"source":"review","resource":{"table":"review_config"},
     "scope":[{"field":"node_type","op":"in","value":[2,3]}],
     "coverage":{"complete":true,"columns":["node_type","enabled"]},
     "rows":[{"node_type":2,"enabled":1}],
     "provenance":{"observed_at":"2026-10-28T00:00:00Z"}},
    {"source":"review","resource":{"table":"review_config"},
     "scope":[{"field":"node_type","op":"in","value":[3,2,2]}],
     "coverage":{"complete":true,"columns":["node_type","enabled"]},
     "rows":[{"node_type":2,"enabled":1}],
     "provenance":{"observed_at":"2026-10-28T00:00:00Z"}}
  ]
}`

// U01：归一化后重复的 scope 必须拒绝（stdout 为空、非零退出）。
func TestTodo26U01DuplicateScopeRejected(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir, snap := writeTodo26Repo(t, "package flow\nfunc Entry(){}\n", todo26U01Snap)
	code, out, errOut := runCLIOutErr(t, "query", "sequence", "symbol:go:example.com/flow:Entry",
		"--code", "--depth", "12", "--format", "mermaid", "--runtime-data", snap, "--repo", dir)
	if code == 0 || strings.TrimSpace(out) != "" {
		t.Fatalf("应失败且 stdout 为空: exit=%d out=%q", code, out)
	}
	want := "error: runtime-data: duplicate normalized scope (source=review, table=review_config, node_type in [2,3])"
	if !strings.Contains(errOut, want) {
		t.Fatalf("stderr 应含 %q，实为 %q", want, errOut)
	}
}

// U02：状态预算耗尽必须明确拒绝，不能把可达的 Hit 标成 excluded。
func TestTodo26U02StateBudgetRejected(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	snap := `{"version":1,"environment":"test","region":"ID","captured_at":"2026-10-28T00:00:00Z","bindings":[],"databases":[]}`
	dir, snapPath := writeTodo26Repo(t, todo26U02Src, snap)
	code, out, errOut := runCLIOutErr(t, "query", "sequence", "symbol:go:example.com/flow:Entry",
		"--code", "--depth", "12", "--format", "mermaid", "--runtime-data", snapPath, "--repo", dir)
	if code == 0 || strings.TrimSpace(out) != "" {
		t.Fatalf("预算耗尽应失败且 stdout 为空: exit=%d out=%q", code, out)
	}
	if !strings.Contains(errOut, "analysis state budget exhausted (64)") {
		t.Fatalf("stderr 应含预算错误，实为 %q", errOut)
	}
}

// U02 取消回归：ctx 已取消时不得产出半棵确定树（stdout 为空）。
func TestTodo26U02CancelNoPartialTree(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	snap := `{"version":1,"environment":"test","region":"ID","captured_at":"2026-10-28T00:00:00Z","bindings":[],"databases":[]}`
	dir, snapPath := writeTodo26Repo(t, todo26U02Src, snap)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	code, out, errOut := runCLIOutErrCtx(t, ctx, "query", "sequence", "symbol:go:example.com/flow:Entry",
		"--code", "--depth", "12", "--format", "mermaid", "--runtime-data", snapPath, "--repo", dir)
	if code == 0 || strings.TrimSpace(out) != "" {
		t.Fatalf("取消应失败且 stdout 为空: exit=%d out=%q", code, out)
	}
	if !strings.Contains(errOut, "error") {
		t.Fatalf("错误应写 stderr: %q", errOut)
	}
}

// T14：解释器步数预算耗尽必须失败（stdout 为空、非零退出），不返回截断
// 后的"成功"结果。
func TestTodo26T14StepBudgetExhausted(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	var b strings.Builder
	b.WriteString("package flow\n\nfunc Entry(){\n")
	for i := 0; i < 10001; i++ {
		b.WriteString("\t_ = 1\n")
	}
	b.WriteString("}\n")
	snap := `{"version":1,"environment":"test","region":"ID","captured_at":"2026-10-28T00:00:00Z","bindings":[],"databases":[]}`
	dir, snapPath := writeTodo26Repo(t, b.String(), snap)
	code, out, errOut := runCLIOutErr(t, "query", "sequence", "symbol:go:example.com/flow:Entry",
		"--code", "--depth", "12", "--format", "mermaid", "--runtime-data", snapPath, "--repo", dir)
	if code == 0 || strings.TrimSpace(out) != "" {
		t.Fatalf("步数预算耗尽应失败且 stdout 为空: exit=%d out=%q", code, out)
	}
	if !strings.Contains(errOut, "budget") {
		t.Fatalf("stderr 应含 budget: %q", errOut)
	}
}
