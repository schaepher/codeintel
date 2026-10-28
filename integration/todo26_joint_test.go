//go:build integration

package integration

// todo26 T18：真实 init→CLI 联合回归——装饰器/回调列表、未验证候选执行统计、
// 输入拒绝与渲染/证明分别断言。

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
)

const todo26DecoratorSrc = `package flow

func BeforeFirst()  {}
func BeforeSecond() {}
func Store()        { Hit() }
func Hit()          {}
func AfterFirst()   {}
func AfterSecond()  {}

func First(f func()) func()  { return func() { BeforeFirst(); f(); AfterFirst() } }
func Second(f func()) func() { return func() { BeforeSecond(); f(); AfterSecond() } }

func Run(f func(), wrappers ...func(func()) func()) {
	for _, w := range wrappers {
		f = w(f)
	}
	f()
}

func Entry() { Run(Store, First, Second) }
`

// u03Walk 收集满足条件的节点。
func t18Walk(nodes []*action.CodeSeqNode, pred func(*action.CodeSeqNode) bool, out *[]*action.CodeSeqNode) {
	for _, n := range nodes {
		if pred(n) {
			*out = append(*out, n)
		}
		t18Walk(n.Nodes, pred, out)
		t18Walk(n.Else, pred, out)
	}
}

func t18Names(nodes []*action.CodeSeqNode, pred func(*action.CodeSeqNode) bool) []string {
	var out []*action.CodeSeqNode
	t18Walk(nodes, pred, &out)
	names := make([]string, 0, len(out))
	for _, n := range out {
		names = append(names, n.Label)
	}
	return names
}

func TestTodo26JointDecoratorCLI(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/flow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), todo26DecoratorSrc)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "init")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit=%d", code)
	}
	// JSON：回调列表路径 + item 顺序 + 装饰器内部调用。
	code, out, errOut := runCLIOutErr(t, "query", "sequence",
		"symbol:go:example.com/flow:Entry", "--code", "--depth", "10", "--json", "--repo", dir)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errOut)
	}
	var root action.CodeSeqNode
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	branches := t18Names(root.Nodes, func(n *action.CodeSeqNode) bool {
		return n.Kind == "branch" && n.Label == "callback list path"
	})
	if len(branches) != 1 {
		t.Fatalf("应有一条 callback list path: %v", branches)
	}
	items := t18Names(root.Nodes, func(n *action.CodeSeqNode) bool {
		return n.Kind == "loop" && strings.HasPrefix(n.Label, "item ")
	})
	if len(items) != 2 || items[0] != "item 1/2" || items[1] != "item 2/2" {
		t.Fatalf("列表项顺序不符: %v", items)
	}
	for _, want := range []string{"BeforeFirst", "BeforeSecond", "Hit", "AfterFirst", "AfterSecond"} {
		if got := t18Names(root.Nodes, func(n *action.CodeSeqNode) bool {
			return n.Kind == "call" && n.Label == want
		}); len(got) != 1 {
			t.Errorf("%s 应执行一次，got %d", want, len(got))
		}
	}
	// 渲染与证明分开断言：Mermaid 可渲染（alt/loop 配对）且包含列表项。
	code, mermaid, errOut := runCLIOutErr(t, "query", "sequence",
		"symbol:go:example.com/flow:Entry", "--code", "--depth", "10", "--format", "mermaid", "--repo", dir)
	if code != 0 {
		t.Fatalf("mermaid exit=%d stderr=%s", code, errOut)
	}
	if !strings.Contains(mermaid, "loop item 1/2") || !strings.Contains(mermaid, "loop item 2/2") {
		t.Errorf("Mermaid 应包含列表项:\n%s", mermaid)
	}
	if got, want := countSeqKeyword(mermaid, "loop "), countSeqKeyword(mermaid, "  end"); got == 0 || got > want {
		t.Errorf("Mermaid 块可能不配对: loop=%d end=%d", got, want)
	}
}

// countSeqKeyword 统计出现次数（渲染配对检查用）。
func countSeqKeyword(s, sub string) int {
	return strings.Count(s, sub)
}

const todo26CandSrc = `package flow

type API interface{ Work() }

type One struct{}
type Two struct{}

func (*One) Work() { HitOne() }
func (*Two) Work() { HitTwo() }
func HitOne()      {}
func HitTwo()      {}

func Entry(v API) { v.Work() }
`

// 未验证候选：断言后代标 Candidate（执行统计排除其所有后代），且不因候选
// 内部的深度被自动续查。
func TestTodo26JointCandidateStatsCLI(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/flow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), todo26CandSrc)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "init")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit=%d", code)
	}
	code, out, errOut := runCLIOutErr(t, "query", "sequence",
		"symbol:go:example.com/flow:Entry", "--code", "--depth", "4", "--json",
		"--continue-segments", "3", "--repo", dir)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errOut)
	}
	var root action.CodeSeqNode
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var calls []*action.CodeSeqNode
	t18Walk(root.Nodes, func(n *action.CodeSeqNode) bool { return n.Kind == "call" }, &calls)
	candConfirmed := 0
	for _, n := range calls {
		if strings.HasPrefix(n.Label, "HitOne") || strings.HasPrefix(n.Label, "HitTwo") {
			if !n.Candidate {
				candConfirmed++
			}
		}
	}
	if candConfirmed != 0 {
		t.Errorf("未验证候选的后代不应计入确定执行（%d 个未标 Candidate）", candConfirmed)
	}
	if root.Meta != nil && root.Meta.Continuation != nil {
		for _, f := range root.Meta.Continuation.Frontiers {
			if strings.Contains(f.TargetID, "Hit") {
				t.Errorf("候选后代不应登记续查前沿: %+v", f)
			}
		}
	}
}

// 输入拒绝：--runtime-data 重复/后接 flag 在开库前拒绝（stdout 为空）。
func TestTodo26JointInputRejectionsCLI(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/flow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), "package flow\n\nfunc Entry() {}\n")
	snap := filepath.Join(dir, "runtime.json")
	writeFile(t, snap, `{"version":1,"environment":"test","region":"ID","captured_at":"2026-10-28T00:00:00Z","bindings":[],"databases":[]}`)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "init")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit=%d", code)
	}
	cases := [][]string{
		{"query", "sequence", "symbol:go:example.com/flow:Entry", "--code", "--json",
			"--runtime-data", snap, "--runtime-data", snap, "--repo", dir},
		{"query", "sequence", "symbol:go:example.com/flow:Entry", "--code", "--json",
			"--runtime-data", "--repo", dir},
		{"query", "sequence", "symbol:go:example.com/flow:Entry", "--code", "--json",
			"--continue-segments", "--evidence", "--repo", dir},
	}
	for _, args := range cases {
		code, out, errOut := runCLIOutErr(t, args...)
		if code == 0 || strings.TrimSpace(out) != "" {
			t.Errorf("参数拒绝失败: args=%v exit=%d out=%q stderr=%q", args, code, out, errOut)
		}
	}
}
