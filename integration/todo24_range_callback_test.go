//go:build integration

package integration

// todo24 §11.5：普通数据 range 内的只读函数参数保持来源（真实 init → CLI）。
//
// 正例：`for _, step := range steps { err = f(ctx); if err == nil { return nil } }`
// ——循环体按**一次**分析（不把重试间隔画成必然执行两次），f 为调用点绑定的
// 回调（corroborated），继续到准确 Sink，保留 range 与 err==nil 条件。
// 反例：循环内改写 f / `Replace(&f)` 取址逃逸 / `for _, f = range others`
// （未知函数列表）都不得确定执行原 Sink。

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
)

const rangeCallbackBase = `package flow

type Context struct{}

func Entry() {
	ctx := Context{}
	req := 1
	reqFunc := func(ctx0 Context) error { Sink(ctx, req); return nil }
	Retry(ctx, []int{1, 2}, reqFunc)
}

func Retry(ctx Context, steps []int, f func(Context) error) (err error) {
	%s
	for _, step := range steps {
		_ = step
		%s
		err = f(ctx)
		if err == nil {
			return nil
		}
	}
	return err
}

%s

func Sink(Context, int) {}
`

// rangeAssignUnknownList 反例：`for _, f = range others`（未知函数列表）。
const rangeAssignUnknownList = `package flow

type Context struct{}

var others []func(Context) error

func Entry() {
	ctx := Context{}
	req := 1
	reqFunc := func(ctx0 Context) error { Sink(ctx, req); return nil }
	Retry(ctx, []int{1, 2}, reqFunc)
}

func Retry(ctx Context, steps []int, f func(Context) error) error {
	for _, step := range steps {
		_ = step
	}
	for _, f = range others {
		f(ctx)
	}
	return nil
}

func Sink(Context, int) {}
`

const rangeCallbackDefer = `defer func() {
		if r := recover(); r != nil {
			err = Failure()
		}
	}()`

func setupRangeCallbackRepo(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/flow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), body)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

func rangeFixture(deferStmt, loopBody, extraDecls string) string {
	return strings.Replace(
		strings.Replace(
			strings.Replace(rangeCallbackBase, "%s", deferStmt, 1),
			"%s", loopBody, 1),
		"%s", extraDecls, 1)
}

// callbackSinkEvidence 找 f 调用节点及其证据。
func callbackSinkEvidence(t *testing.T, root *action.CodeSeqNode) (*action.CodeSeqNode, *action.CodeSeqNode) {
	t.Helper()
	var fNode, sinkNode *action.CodeSeqNode
	walkSeq(root, func(n *action.CodeSeqNode) {
		if n.Kind != "call" {
			return
		}
		if n.Label == "f" {
			fNode = n
		}
		if n.Evidence != nil && strings.HasSuffix(n.Evidence.SymbolID, ":Sink") {
			sinkNode = n
		}
	})
	return fNode, sinkNode
}

func TestRangeReadOnlyCallbackKeepsSource(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := setupRangeCallbackRepo(t, rangeFixture("", "", ""))
	root := seqTreeJSON(t, dir, "symbol:go:example.com/flow:Entry", 8, "--evidence")
	fNode, sink := callbackSinkEvidence(t, root)
	if fNode == nil || fNode.Evidence == nil {
		t.Fatalf("循环体内 f 调用缺失: %+v", root)
	}
	if fNode.Evidence.Ambiguous || fNode.Evidence.Verdict != "corroborated" {
		t.Errorf("f 应为已绑定回调（corroborated），got %+v", fNode.Evidence)
	}
	if !strings.Contains(fNode.Evidence.Reason, "callback") {
		t.Errorf("证据 reason 应体现回调绑定: %q", fNode.Evidence.Reason)
	}
	if sink == nil {
		t.Errorf("应继续到准确 Sink")
	}
	if sink != nil && sink.Evidence.Confidence < 0.8 {
		t.Errorf("Sink 调用边置信度过低: %+v", sink.Evidence)
	}
	// 循环体只分析一次（不把两个重试间隔画成必然执行两次）。
	if got := countSeqSym(root, ":Sink"); got != 1 {
		t.Errorf("Sink 应恰好一次（循环不按数据元素展开），got %d", got)
	}
	var sawRange, sawGuard bool
	walkSeq(root, func(n *action.CodeSeqNode) {
		if n.Kind == "loop" && strings.Contains(n.Label, "range steps") {
			sawRange = true
		}
		if n.Kind == "branch" && strings.Contains(n.Label, "err == nil") {
			sawGuard = true
		}
	})
	if !sawRange || !sawGuard {
		t.Errorf("应保留 range 与 err==nil 条件（range=%v guard=%v）", sawRange, sawGuard)
	}
	// 祖先没有 invocation-unverified。
	walkSeq(root, func(n *action.CodeSeqNode) {
		if n.Stop != nil && n.Stop.Code == "func_value_unbound" {
			t.Errorf("正例不应有 func_value_unbound: %+v", n.Stop)
		}
	})
}

func TestRangeReadOnlyCallbackWithoutDefer(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := setupRangeCallbackRepo(t, rangeFixture("", "", ""))
	root := seqTreeJSON(t, dir, "symbol:go:example.com/flow:Entry", 8)
	if got := countSeqSym(root, ":Sink"); got != 1 {
		t.Errorf("去掉 defer 后仍应到 Sink 一次，got %d", got)
	}
}

func TestRangeCallbackCounterExamples(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	cases := []struct {
		name     string
		body     string
		extra    string
		wantSink int
	}{
		{
			name: "loop-overwrite",
			body: "f = func(Context) error { return nil };\n\t\t",
		},
		{
			name:  "addr-escape",
			body:  "Replace(&f);\n\t\t",
			extra: "func Replace(*func(Context) error) {}",
		},
		{
			name: "range-assign-unknown-list",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := rangeFixture("", c.body, c.extra)
			if c.name == "range-assign-unknown-list" {
				// 未知函数列表不得回退成循环前的 f，也不得确定执行原 Sink。
				src = rangeAssignUnknownList
			}
			dir := setupRangeCallbackRepo(t, src)
			root := seqTreeJSON(t, dir, "symbol:go:example.com/flow:Entry", 8)
			if got := countSeqSym(root, ":Sink"); got != c.wantSink {
				t.Errorf("%s: Sink 应 %d 次，got %d: %+v", c.name, c.wantSink, got, root)
			}
		})
	}
}
