package action

// T01：调用求值顺序（实参先于外层调用）、defer/go 实参同步求值、
// if 初始化语句与直接 IIFE 的调用展开。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

func seedT01(t *testing.T, src string, nodes []*domain.CodeEntity, facts []*domain.Fact) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.26.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "flow.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatalf("save: %v", err)
	}
	return New(r), dir
}

// flatSeqCalls 按展开顺序收集 call 节点标签（深度优先，先 Nodes 后 Else）。
func flatSeqCalls(nodes []*CodeSeqNode, out *[]*CodeSeqNode) {
	for _, n := range nodes {
		if n.Kind == "call" {
			*out = append(*out, n)
		}
		flatSeqCalls(n.Nodes, out)
		flatSeqCalls(n.Else, out)
	}
}

func labelsOf(nodes []*CodeSeqNode) []string {
	var calls []*CodeSeqNode
	flatSeqCalls(nodes, &calls)
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		out = append(out, c.Label)
	}
	return out
}

const t01Src = `package m

func Build() int { Leaf(); return 1 }
func Build2() int { Leaf(); return 2 }
func Build3() int { Leaf(); return 3 }
func Leaf() {}
func Use(int) {}
func Use2(int) {}
func Use3(int) {}

func Entry() {
	Use(Build())
	defer Use2(Build2())
	go Use3(Build3())
}
`

func t01Nodes() []*domain.CodeEntity {
	mk := func(name string, line int) *domain.CodeEntity {
		return &domain.CodeEntity{ID: domain.CanonicalID("symbol:go:example.com/m:" + name),
			Kind: domain.KindFunction, Name: name, FilePath: "flow.go", LineStart: line}
	}
	return []*domain.CodeEntity{
		mk("Build", 3), mk("Build2", 4), mk("Build3", 5), mk("Leaf", 6),
		mk("Use", 7), mk("Use2", 8), mk("Use3", 9), mk("Entry", 11),
	}
}

func t01Facts() []*domain.Fact {
	fact := func(src, dst string, line int) *domain.Fact {
		return &domain.Fact{SourceID: domain.CanonicalID("symbol:go:example.com/m:" + src),
			TargetID: domain.CanonicalID("symbol:go:example.com/m:" + dst),
			Kind:     domain.FactCalls, Confidence: 0.8,
			Metadata: map[string]any{"line_num": line}}
	}
	return []*domain.Fact{
		fact("Entry", "Use", 12), fact("Entry", "Use2", 13), fact("Entry", "Use3", 14),
		fact("Build", "Leaf", 3), fact("Build2", "Leaf", 4), fact("Build3", "Leaf", 5),
	}
}

// 实参调用先于外层调用；defer/go 的实参在当前流程求值，延迟/异步标记
// 只加在被调度的调用上。
func TestCodeSequenceEvalOrderDeferGo(t *testing.T) {
	acts, dir := seedT01(t, t01Src, t01Nodes(), t01Facts())
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "symbol:go:example.com/m:Entry",
		RepoAbs: dir, Depth: 3})
	if err != nil {
		t.Fatal(err)
	}
	var calls []*CodeSeqNode
	flatSeqCalls(root.Nodes, &calls)
	// 顶层顺序：Build, Use, Build2, Use2(defer), Build3, Use3(async)
	var top []*CodeSeqNode
	for _, c := range root.Nodes {
		if c.Kind == "call" {
			top = append(top, c)
		}
	}
	got := make([]string, 0, len(top))
	for _, c := range top {
		got = append(got, c.Label)
	}
	want := []string{"Build", "Use", "Build2", "Use2", "Build3", "Use3"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("调用顺序 = %v, want %v（实参先于外层调用）", got, want)
	}
	if top[1].Exec != "" {
		t.Errorf("Use 是同步调用，Exec = %q", top[1].Exec)
	}
	if top[3].Exec != SeqExecDefer {
		t.Errorf("Use2 应为 defer，Exec = %q", top[3].Exec)
	}
	if top[5].Exec != SeqExecAsync {
		t.Errorf("Use3 应为 async，Exec = %q", top[5].Exec)
	}
}

const t01IfSrc = `package m

func Build2() int { return 2 }
func Use(int) {}
func Hit() {}

func Entry() {
	if v := Build2(); v > 0 {
		Use(v)
	}
	func() { Hit() }()
}
`

func TestCodeSequenceIfInitAndIIFE(t *testing.T) {
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:Build2", Kind: domain.KindFunction, Name: "Build2", FilePath: "flow.go", LineStart: 3},
		{ID: "symbol:go:example.com/m:Use", Kind: domain.KindFunction, Name: "Use", FilePath: "flow.go", LineStart: 4},
		{ID: "symbol:go:example.com/m:Hit", Kind: domain.KindFunction, Name: "Hit", FilePath: "flow.go", LineStart: 5},
		{ID: "symbol:go:example.com/m:Entry", Kind: domain.KindFunction, Name: "Entry", FilePath: "flow.go", LineStart: 7},
	}
	facts := []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:Entry", TargetID: "symbol:go:example.com/m:Use",
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": 9}},
	}
	acts, dir := seedT01(t, t01IfSrc, nodes, facts)
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "symbol:go:example.com/m:Entry",
		RepoAbs: dir, Depth: 3})
	if err != nil {
		t.Fatal(err)
	}
	labels := labelsOf(root.Nodes)
	joined := strings.Join(labels, ",")
	if !strings.Contains(joined, "Build2") {
		t.Errorf("if 初始化语句里的调用应展开: %v", labels)
	}
	if strings.Index(joined, "Build2") > strings.Index(joined, "Use") {
		t.Errorf("if 初始化调用应先于条件体调用: %v", labels)
	}
	if !strings.Contains(joined, "Hit") {
		t.Errorf("直接 IIFE 体应展开: %v", labels)
	}
}

// retry 包装：局部变量保存的闭包经包装函数实际调用后仍能证明 Hit；
// 调用前被未知覆盖/取址后不再沿用旧证明。
func TestCodeSequenceLocalClosureRetryWrapper(t *testing.T) {
	const pkg = "symbol:go:example.com/m:"
	base := map[string]string{"Retry": pkg + "Retry", "Hit": pkg + "Hit", "Replace": pkg + "Replace"}
	cases := []struct {
		name    string
		body    string
		wantHit int
	}{
		{"invoked", `f := func(){ Hit() }
	Retry(func(){ f() })`, 1},
		{"overwritten-unknown", `f := func(){ Hit() }
	f = unknown
	Retry(func(){ f() })`, 0},
		{"address-taken", `f := func(){ Hit() }
	Replace(&f)
	Retry(func(){ f() })`, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := `package m

func Retry(fn func()) { fn() }
func Replace(p *func()) { *p = func(){} }
func unknown() {}
func Hit() {}

func Entry() {
	` + c.body + `
}
`
			acts, dir := seedFuncValueRepo(t, src, base)
			root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Entry", RepoAbs: dir, Depth: 4})
			if err != nil {
				t.Fatalf("CodeSequence: %v", err)
			}
			if got := len(seqCallNodesNamed(root, "Hit")); got != c.wantHit {
				t.Errorf("%s: Hit 执行 %d 次，want %d（调用前改写/取址应失效）", c.name, got, c.wantHit)
			}
		})
	}
}

// 递归身份：同一 executor 接收不同回调不是 cycle；回调上下文里真正递归
// 同一 executor 仍停止并标 cycle。
func TestCodeSequenceCallbackCycleIdentity(t *testing.T) {
	const pkg = "symbol:go:example.com/m:"
	src := `package m

func Execute(fn func()) { fn() }
func Hit() {}

func Entry() {
	Execute(func(){ Execute(func(){ Hit() }) })
}
`
	acts, dir := seedFuncValueRepo(t, src, map[string]string{
		"Execute": pkg + "Execute", "Hit": pkg + "Hit",
	})
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Entry", RepoAbs: dir, Depth: 6})
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}
	if got := len(seqCallNodesNamed(root, "Hit")); got != 1 {
		t.Errorf("嵌套回调应执行 Hit 一次，got %d", got)
	}
	for _, n := range seqFindNodes(root, func(n *CodeSeqNode) bool {
		return n.Stop != nil && n.Stop.Code == SeqStopCycle
	}) {
		t.Errorf("同一 executor 的不同回调上下文不应误判 cycle: %+v", n.Stop)
	}

	rec := `package m

func Execute(fn func()) { fn() }
func Entry() {
	Execute(func(){ Entry() })
}
`
	acts2, dir2 := seedFuncValueRepo(t, rec, map[string]string{"Execute": pkg + "Execute"})
	root2, err := acts2.CodeSequence(CodeSequenceRequest{Target: "Entry", RepoAbs: dir2, Depth: 8})
	if err != nil {
		t.Fatalf("CodeSequence(recursive): %v", err)
	}
	if len(seqFindNodes(root2, func(n *CodeSeqNode) bool {
		return n.Stop != nil && n.Stop.Code == SeqStopCycle
	})) == 0 {
		t.Errorf("真正的递归应标 cycle")
	}
}
