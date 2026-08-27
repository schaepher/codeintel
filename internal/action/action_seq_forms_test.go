package action

// R100 续：被丢弃的正常代码形态排查——walkStmt 只认顶层 CallExpr，
// 以下形态整条/调用丢失：
//   - return foo()（函数体单行返回调用——调用显示但返回线丢失）
//   - return foo() + 1 / x = foo() + bar()（调用嵌套在表达式内）
//   - ch <- foo()（SendStmt 无 case 整条丢弃）
//   - select { ... }（SelectStmt 无 case 整块丢弃）
//   - Loop: for ...（LabeledStmt 无 case 整块丢弃）

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedSeqFormsRepo fixture：六种形态 + 全部函数节点（LineStart 对齐）。
func seedSeqFormsRepo(t *testing.T) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package m

import "example.com/m/svc"

func returnCall() {
	return svc.Get()
}

func returnExpr() int {
	return svc.Get() + 1
}

func assignExpr() {
	x := svc.Get() + svc.Load(1)
	y = svc.Save(2)
}

func sendVal() {
	ch <- svc.Get()
}

func selectCases() {
	select {
	case v := <-ch:
		svc.Use(v)
	case ch2 <- svc.Get():
	}
}

func labeled() {
Loop:
	for {
		svc.Poll()
	}
}

var ch chan int
var ch2 chan int
var y int
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:returnCall", Kind: domain.KindFunction, Name: "returnCall", FilePath: "main.go", LineStart: 5},
		{ID: "symbol:go:example.com/m:returnExpr", Kind: domain.KindFunction, Name: "returnExpr", FilePath: "main.go", LineStart: 9},
		{ID: "symbol:go:example.com/m:assignExpr", Kind: domain.KindFunction, Name: "assignExpr", FilePath: "main.go", LineStart: 13},
		{ID: "symbol:go:example.com/m:sendVal", Kind: domain.KindFunction, Name: "sendVal", FilePath: "main.go", LineStart: 18},
		{ID: "symbol:go:example.com/m:selectCases", Kind: domain.KindFunction, Name: "selectCases", FilePath: "main.go", LineStart: 22},
		{ID: "symbol:go:example.com/m:labeled", Kind: domain.KindFunction, Name: "labeled", FilePath: "main.go", LineStart: 30},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	return New(r), dir
}

// seqTopCalls 取根节点顶层 call 的 label 列表。
func seqTopCalls(t *testing.T, acts *Actions, dir, target string) []string {
	t.Helper()
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: target, RepoAbs: dir, Depth: 1})
	if err != nil {
		t.Fatalf("%s: %v", target, err)
	}
	if root == nil {
		t.Fatalf("%s: 解析 nil", target)
	}
	var out []string
	for _, n := range root.Nodes {
		out = append(out, n.Kind+":"+n.Label)
	}
	return out
}

// TestCodeSequenceReturnCall：函数体单行 return 调用——调用显示。
func TestCodeSequenceReturnCall(t *testing.T) {
	acts, dir := seedSeqFormsRepo(t)
	got := seqTopCalls(t, acts, dir, "returnCall")
	if len(got) != 1 || got[0] != "call:svc.Get" {
		t.Errorf("returnCall 顶层 = %v; want [call:svc.Get]", got)
	}
}

// TestCodeSequenceReturnExpr：return foo() + 1——嵌套调用提取（不丢）。
func TestCodeSequenceReturnExpr(t *testing.T) {
	acts, dir := seedSeqFormsRepo(t)
	got := seqTopCalls(t, acts, dir, "returnExpr")
	if len(got) != 1 || got[0] != "call:svc.Get" {
		t.Errorf("returnExpr 顶层 = %v; want [call:svc.Get]（BinaryExpr 内调用）", got)
	}
}

// TestCodeSequenceAssignExpr：x = foo() + bar()——同 RHS 多调用都提取。
func TestCodeSequenceAssignExpr(t *testing.T) {
	acts, dir := seedSeqFormsRepo(t)
	got := seqTopCalls(t, acts, dir, "assignExpr")
	want := []string{"call:svc.Get", "call:svc.Load", "call:svc.Save"}
	if len(got) != len(want) {
		t.Fatalf("assignExpr 顶层 = %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("assignExpr[%d] = %q; want %q（表达式内调用提取）", i, got[i], want[i])
		}
	}
}

// TestCodeSequenceSendStmt：ch <- foo()——send 不整条丢弃，调用显示。
func TestCodeSequenceSendStmt(t *testing.T) {
	acts, dir := seedSeqFormsRepo(t)
	got := seqTopCalls(t, acts, dir, "sendVal")
	if len(got) != 1 || got[0] != "call:svc.Get" {
		t.Errorf("sendVal 顶层 = %v; want [call:svc.Get]（SendStmt 提取）", got)
	}
}

// TestCodeSequenceSelect：select 多路复用——分支结构 + 各 case 内调用。
func TestCodeSequenceSelect(t *testing.T) {
	acts, dir := seedSeqFormsRepo(t)
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "selectCases", RepoAbs: dir, Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(root.Nodes) != 1 || root.Nodes[0].Kind != "branch" {
		t.Fatalf("selectCases 顶层 = %v; want [branch]", root.Nodes)
	}
	cases := root.Nodes[0].Nodes
	if len(cases) != 2 {
		t.Fatalf("select 分支数 = %d; want 2: %+v", len(cases), cases)
	}
	if !strings.Contains(cases[0].Label, "case") {
		t.Errorf("case 1 label = %q; want case 前缀", cases[0].Label)
	}
	if len(cases[0].Nodes) != 1 || cases[0].Nodes[0].Label != "svc.Use" {
		t.Errorf("case 1 体内调用缺失: %+v", cases[0].Nodes)
	}
	if len(cases[1].Nodes) != 1 || cases[1].Nodes[0].Label != "svc.Get" {
		t.Errorf("case 2 体内调用缺失（send 分支）: %+v", cases[1].Nodes)
	}
}

// TestCodeSequenceLabeled：label: for——label 不整块丢弃，内部调用显示。
func TestCodeSequenceLabeled(t *testing.T) {
	acts, dir := seedSeqFormsRepo(t)
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "labeled", RepoAbs: dir, Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(root.Nodes) != 1 || root.Nodes[0].Kind != "loop" {
		t.Fatalf("labeled 顶层 = %v; want [loop]（LabeledStmt 递归到 for）", root.Nodes)
	}
	if len(root.Nodes[0].Nodes) != 1 || root.Nodes[0].Nodes[0].Label != "svc.Poll" {
		t.Errorf("loop 体内调用缺失: %+v", root.Nodes[0].Nodes)
	}
}
