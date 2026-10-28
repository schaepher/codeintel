//go:build integration

package integration

// todo24 §2：`query sequence --code --continue-segments N`——在原调用上下文里
// 分段续查 depth 前沿（真实 init → CLI）。公开夹具见 docs/field_trace.md §152。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
)

const continuationGoMod = "module example.com/continuation\n\ngo 1.25\n"

const continuationFlow = `package continuation

func Entry() {
	f := func() { Sink() }
	First(f)
	f = func() { Wrong() }
	_ = f
}

func First(f func())   { Second(f) }
func Second(f func())  { Execute(f) }
func Execute(f func()) { f() }
func Sink()            {}
func Wrong()           {}

func DifferentEntry() { Execute(func() { Middle() }) }
func Middle()         { Execute(func() { Sink() }) }
func LoopEntry()      { Again(func() { Wrong() }) }
func Again(f func())  { Again(f) }

func GuardEntry() { Choose(1) }
func Choose(kind int) {
	if kind == 1 {
		First(func() { Sink() })
	} else {
		First(func() { Wrong() })
	}
}

func DeferredEntry() {
	defer First(func() { Sink() })
	go First(func() { Sink() })
}
`

func setupContinuationRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), continuationGoMod)
	writeFile(t, filepath.Join(dir, "flow.go"), continuationFlow)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "init")
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

// continueSym 夹具里的符号 canonical ID。
func continueSym(name string) string { return "symbol:go:example.com/continuation:" + name }

func TestContinueSegmentsRealCLI(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := setupContinuationRepo(t)
	entry := continueSym("Entry")

	// ① 未启用：只到 First，Sink 不出现，First 留有 depth 诊断。
	base := seqTreeJSON(t, dir, entry, 1)
	if base.Meta != nil && base.Meta.Continuation != nil {
		t.Errorf("未启用续查不得输出 meta.continuation: %+v", base.Meta.Continuation)
	}
	if len(base.Nodes) != 1 {
		t.Fatalf("depth=1 应只有 First 一层: %+v", base.Nodes)
	}
	first := base.Nodes[0]
	if first.Evidence == nil || first.Evidence.SymbolID != continueSym("First") {
		t.Fatalf("First 目标 ID 不准确: %+v", first.Evidence)
	}
	if first.Stop == nil || first.Stop.Code != "depth" {
		t.Errorf("First 应停在 depth 诊断: %+v", first.Stop)
	}
	if countSeqSym(base, ":Sink") != 0 {
		t.Errorf("depth=1 不应出现 Sink")
	}

	// ② 续查 16 段：Entry→First→Second→Execute→原回调→Sink；Wrong 零。
	root := seqTreeJSON(t, dir, entry, 1, "--continue-segments", "16", "--evidence")
	for _, want := range []string{":First", ":Second", ":Execute", ":Sink"} {
		if countSeqSym(root, want) != 1 {
			t.Errorf("%s 应恰好出现 1 次（got %d）", want, countSeqSym(root, want))
		}
	}
	if countSeqSym(root, ":Wrong") != 0 {
		t.Errorf("调用之后的覆写不得污染调用之前的绑定（Wrong 出现 %d 次）", countSeqSym(root, ":Wrong"))
	}
	if root.Meta == nil || root.Meta.Continuation == nil {
		t.Fatalf("缺少 meta.continuation")
	}
	meta := root.Meta.Continuation
	if meta.SegmentsBudget != 16 || meta.SegmentsUsed != 4 || meta.BudgetStops != 0 {
		t.Errorf("meta.continuation = %+v，期望 budget=16 used=4 budget_stops=0", meta)
	}
	var sawFirst bool
	for _, f := range meta.Frontiers {
		if f.TargetID == continueSym("First") {
			sawFirst = true
			if f.CallFile != "flow.go" || f.CallLine == 0 || f.Repo != dir {
				t.Errorf("前沿应带调用位置/仓库: %+v", f)
			}
			if !strings.Contains(f.DepthReason, "depth 上限") {
				t.Errorf("前沿应保留原 depth 原因: %q", f.DepthReason)
			}
		}
	}
	if !sawFirst {
		t.Errorf("meta.continuation 应含 First 前沿: %+v", meta.Frontiers)
	}

	// ③ JSON 与 Mermaid 共用同一棵树（路径一致）。
	var labels, syms []string
	seqPreOrder(root, &labels, &syms)
	code, mmd := runCLIOut(t, "query", "sequence", entry, "--repo", dir, "--code",
		"--depth", "1", "--continue-segments", "16", "--format", "mermaid")
	if code != 0 {
		t.Fatalf("mermaid exit=%d\n%s", code, mmd)
	}
	order := mermaidCallOrder(mmd)
	// Mermaid 的箭头从入口参与者开始（JSON 先序第一个是入口节点本身）。
	wantOrder := labels
	if len(wantOrder) > 0 {
		wantOrder = wantOrder[1:]
	}
	if strings.Join(order, ">") != strings.Join(wantOrder, ">") {
		t.Errorf("Mermaid 调用顺序与 JSON 不一致:\nJSON:   %v\nMermaid:%v\n%s", wantOrder, order, mmd)
	}

	// ④ 段数预算 1：Sink 零次、剩余前沿仍是 depth、明确段数耗尽。
	one := seqTreeJSON(t, dir, entry, 1, "--continue-segments", "1")
	if countSeqSym(one, ":Sink") != 0 {
		t.Errorf("--continue-segments 1 不应展开到 Sink")
	}
	if one.Meta == nil || one.Meta.Continuation == nil || one.Meta.Continuation.BudgetStops != 1 {
		t.Errorf("预算耗尽应记 budget_stops=1: %+v", one.Meta)
	}
	if seqDiagnosticCount(one, "continuation_budget") != 1 {
		t.Errorf("应有 continuation_budget 诊断: %+v", one.Meta.Diagnostics)
	}
	if seqDiagnosticCount(one, "continuation_budget") == 1 && seqDiagnosticCount(one, "depth") == 0 {
		t.Errorf("剩余前沿应保留 depth 诊断")
	}

	// ⑤ DifferentEntry：不同入站回调的两个 Execute 都展开，Sink 一次，cycle 零。
	diff := seqTreeJSON(t, dir, continueSym("DifferentEntry"), 1, "--continue-segments", "16")
	if countSeqSym(diff, ":Execute") != 2 || countSeqSym(diff, ":Sink") != 1 {
		t.Errorf("DifferentEntry: Execute 应 2 次/Sink 1 次，got %d/%d",
			countSeqSym(diff, ":Execute"), countSeqSym(diff, ":Sink"))
	}
	if seqDiagnosticCount(diff, "cycle") != 0 {
		t.Errorf("DifferentEntry 不应有 cycle 诊断")
	}

	// ⑥ LoopEntry：Wrong 零次，递归处 cycle 恰好一次（不靠耗尽段数）。
	loop := seqTreeJSON(t, dir, continueSym("LoopEntry"), 1, "--continue-segments", "16")
	if countSeqSym(loop, ":Wrong") != 0 {
		t.Errorf("LoopEntry 不应执行 Wrong")
	}
	if got := seqDiagnosticCount(loop, "cycle"); got != 1 {
		t.Errorf("LoopEntry 应恰好 1 次 cycle 诊断，got %d", got)
	}
	if loop.Meta.Continuation.BudgetStops != 0 {
		t.Errorf("循环应由 cycle 截断而不是耗尽段数: %+v", loop.Meta.Continuation)
	}

	// ⑦ GuardEntry + --scene kind=1：Sink 一次、Wrong 零次，保留场景依据。
	guard := seqTreeJSON(t, dir, continueSym("GuardEntry"), 1, "--continue-segments", "16",
		"--scene", "kind=1")
	if countSeqSym(guard, ":Sink") != 1 || countSeqSym(guard, ":Wrong") != 0 {
		t.Errorf("GuardEntry scene=1: Sink/Wrong = %d/%d",
			countSeqSym(guard, ":Sink"), countSeqSym(guard, ":Wrong"))
	}
	if guard.Meta == nil || guard.Meta.Scene == nil || !guard.Meta.Scene.Applied {
		t.Errorf("应保留场景依据（meta.scene.applied）: %+v", guard.Meta)
	}
	// 未知运行时守卫：保留互斥条件（then/else 都在），不谎称串行
	unknown := seqTreeJSON(t, dir, continueSym("GuardEntry"), 1, "--continue-segments", "16")
	var branches int
	walkSeq(unknown, func(n *action.CodeSeqNode) {
		if n.Kind == "branch" {
			branches++
		}
	})
	if branches != 1 || len(unknown.Nodes[0].Nodes[0].Else) == 0 {
		t.Errorf("未知守卫应保留互斥分支（branch=%d）: %+v", branches, unknown)
	}

	// ⑧ DeferredEntry：Sink 两次，defer/async 语义保留（不伪造完成顺序）。
	deferred := seqTreeJSON(t, dir, continueSym("DeferredEntry"), 1, "--continue-segments", "16")
	if countSeqSym(deferred, ":Sink") != 2 {
		t.Errorf("DeferredEntry 应执行 Sink 两次，got %d", countSeqSym(deferred, ":Sink"))
	}
	var execs []string
	for _, n := range deferred.Nodes {
		execs = append(execs, n.Exec)
	}
	if len(execs) != 2 || execs[0] != "defer" || execs[1] != "async" {
		t.Errorf("defer/go 标记应保留，got %v", execs)
	}

	// ⑨ 查询预算 1ns：非零退出且 stdout 为空（不输出半棵树）。
	code, stdout, stderr := runCLIOutErr(t, "query", "sequence", entry, "--repo", dir,
		"--code", "--depth", "1", "--continue-segments", "16", "--timeout", "1ns", "--json")
	if code == 0 || strings.TrimSpace(stdout) != "" {
		t.Errorf("1ns 预算应失败且 stdout 为空: exit=%d stdout=%q", code, stdout)
	}
	if !strings.Contains(stderr, "预算") && !strings.Contains(stderr, "取消") {
		t.Errorf("预算失败应说明原因: %q", stderr)
	}
}

// TestContinueSegmentsEvidenceIdentity：续查选项进入结构化 argv、可复制命令与
// config_hash；未启用时保持原请求身份（无 continuation 键、argv 不变）。
func TestContinueSegmentsEvidenceIdentity(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := setupContinuationRepo(t)
	entry := continueSym("Entry")

	off := seqTreeJSON(t, dir, entry, 1, "--evidence")
	on := seqTreeJSON(t, dir, entry, 1, "--continue-segments", "3", "--evidence")
	if off.Meta == nil || off.Meta.Evidence == nil || on.Meta == nil || on.Meta.Evidence == nil {
		t.Fatalf("缺少 meta.evidence")
	}
	offEv, onEv := off.Meta.Evidence, on.Meta.Evidence
	if offEv.ContinueSegments != 0 || onEv.ContinueSegments != 3 {
		t.Errorf("证据身份应记录续查段数: off=%d on=%d", offEv.ContinueSegments, onEv.ContinueSegments)
	}
	if strings.Contains(strings.Join(offEv.Argv, " "), "--continue-segments") {
		t.Errorf("未启用时 argv 不应出现 --continue-segments: %v", offEv.Argv)
	}
	if !strings.Contains(strings.Join(onEv.Argv, " "), "--continue-segments 3") {
		t.Errorf("启用时 argv 应含 --continue-segments 3: %v", onEv.Argv)
	}
	if offEv.ConfigHash == onEv.ConfigHash {
		t.Errorf("续查选项应进入 config_hash（off=%s on=%s）", offEv.ConfigHash, onEv.ConfigHash)
	}
	// 可复制命令（结构化 argv 元素不含 shell 引号，可直接 execve）
	for _, a := range onEv.Argv {
		if strings.ContainsAny(a, "'\"") {
			t.Errorf("argv 元素不得含 shell 引号: %q", a)
		}
	}
	// JSON 序列化里 meta 不含 continuation 键（未启用）
	raw, _ := json.Marshal(off)
	if strings.Contains(string(raw), `"continuation"`) {
		t.Errorf("未启用时 JSON 不得出现 continuation 键")
	}
	_ = os.Stdout
}
