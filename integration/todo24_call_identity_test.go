//go:build integration

package integration

// todo24 §11.6：汇总调用边不得混用实例方法与包函数（真实 init → CLI）。
//
// 夹具：`logger` 是 `example.com/flow/log` 的导入别名（包函数 Error(string)），
// 同模块另有 `(*Worker).Error()`。Entry 两次合法包函数调用 + 一次未知 error
// 实现的 `err.Error()`；MethodEntry/ShadowEntry/NestedEntry 分别是 receiver、
// 遮蔽导入别名的局部变量、嵌套 receiver 的方法调用。

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
)

const callIdentityModule = `package flow

import logger "example.com/flow/log"

func Entry(err error) {
	logger.Error("first")
	consume(err.Error())
	logger.Error("repeat")
}

func consume(string) {}

type Worker struct{}

func (*Worker) Error() string { return "worker" }

func MethodEntry(w *Worker) { w.Error() }

func ShadowEntry(w *Worker) { logger := w; logger.Error() }

func NestedEntry(w *Worker) {
	box := struct{ worker *Worker }{w}
	box.worker.Error()
}
`

func setupCallIdentityRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/flow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), callIdentityModule)
	writeFile(t, filepath.Join(dir, "log", "log.go"), "package log\n\nfunc Error(string) {}\n")
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

func seqNodesByLabel(root *action.CodeSeqNode, label string) []*action.CodeSeqNode {
	var out []*action.CodeSeqNode
	walkSeq(root, func(n *action.CodeSeqNode) {
		if n.Kind == "call" && n.Label == label {
			out = append(out, n)
		}
	})
	return out
}

func TestSeqCallIdentityRealCLI(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := setupCallIdentityRepo(t)

	// Entry：两处 logger.Error 都是包函数 log:Error；err.Error 不得借用它。
	entry := seqTreeJSON(t, dir, "symbol:go:example.com/flow:Entry", 1)
	pkgCalls := seqNodesByLabel(entry, `logger.Error`)
	if len(pkgCalls) != 2 {
		t.Fatalf("Entry 应保留两处 logger.Error，got %d（root=%+v）", len(pkgCalls), entry)
	}
	for i, n := range pkgCalls {
		if n.Evidence == nil || n.Evidence.SymbolID != "symbol:go:example.com/flow/log:Error" {
			t.Errorf("logger.Error[%d] 目标应为 log:Error，got %+v", i, n.Evidence)
		}
	}
	errCalls := seqNodesByLabel(entry, `err.Error`)
	if len(errCalls) != 1 {
		t.Fatalf("Entry 应保留一处 err.Error，got %d", len(errCalls))
	}
	if ev := errCalls[0].Evidence; ev != nil {
		if ev.SymbolID == "symbol:go:example.com/flow/log:Error" {
			t.Errorf("err.Error 不得借用包函数 log:Error: %+v", ev)
		}
		if strings.Contains(ev.Reason, "aggregated") || strings.Contains(ev.Reason, "corroborated") {
			t.Errorf("err.Error 不得带 index-call-edge-aggregated/corroborated 证据: %+v", ev)
		}
	}

	// 三个实例 selector 入口：指向 (Worker).Error，绝不借用 log:Error。
	cases := map[string]string{
		"MethodEntry": `w.Error`,
		"ShadowEntry": `logger.Error`,
		"NestedEntry": `box.worker.Error`,
	}
	for fn, label := range cases {
		root := seqTreeJSON(t, dir, "symbol:go:example.com/flow:"+fn, 1)
		nodes := seqNodesByLabel(root, label)
		if len(nodes) != 1 {
			t.Errorf("%s 应有 1 个 %s 调用，got %d（%+v）", fn, label, len(nodes), root)
			continue
		}
		ev := nodes[0].Evidence
		if ev == nil || ev.SymbolID != "symbol:go:example.com/flow:(Worker).Error" {
			t.Errorf("%s: %s 目标应为 (Worker).Error，got %+v", fn, label, ev)
		}
	}

	// 同步验收图输出：Mermaid 里实例调用不得出现 log 包函数身份。
	code, mmd := runCLIOut(t, "query", "sequence", "symbol:go:example.com/flow:ShadowEntry",
		"--code", "--depth", "1", "--format", "mermaid", "--repo", dir)
	if code != 0 {
		t.Fatalf("mermaid exit=%d\n%s", code, mmd)
	}
	if strings.Contains(mmd, "log:Error") {
		t.Errorf("ShadowEntry 的 Mermaid 不得把局部变量调用画成包函数 log:Error:\n%s", mmd)
	}
}
