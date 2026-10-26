package action

// todo10-2：代码级时序的执行语义与返回语义。
//   - defer：标注"退出时执行"（位置仍在声明处，不得表现为此处立即解锁）
//   - go：标注"并发启动"（完成顺序未知，不得表现为同步完成）
//   - return：无论是否含调用都输出独立 return 节点（值/结果槽/终止路径），
//     返回表达式内的调用保留求值顺序，返回之后的语句不属于该路径
//   - --focus：只保留影响指定状态字段/返回值的赋值与条件（复用 SSA 摘要）

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

const seqSemanticsSrc = `package m

type Resp struct {
	Status string
}

func call() *Resp { return &Resp{Status: "ok"} }

func audit() {}

func unlock() {}

func handle() {}

func wrap() *Resp {
	out := &Resp{Status: "pending"}
	unlock()
	defer unlock()
	go handle()
	resp := call()
	out.Status = resp.Status
	audit()
	return out
}

func early() *Resp {
	defer unlock()
	return call()
}

func deadCode() *Resp {
	return call()
	audit()
}
`

// seedSeqSemanticsRepo 建执行/返回语义 fixture 并写入索引（含 wrap 的
// SSA 摘要行：out.Status 的写入行）。
func seedSeqSemanticsRepo(t *testing.T) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(seqSemanticsSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:wrap", Kind: domain.KindFunction, Name: "wrap", FilePath: "main.go", LineStart: 15},
		{ID: "symbol:go:example.com/m:early", Kind: domain.KindFunction, Name: "early", FilePath: "main.go", LineStart: 26},
		{ID: "symbol:go:example.com/m:deadCode", Kind: domain.KindFunction, Name: "deadCode", FilePath: "main.go", LineStart: 31},
	}
	sums := []*domain.FunctionFieldSummary{
		// out.Status = resp.Status（第 21 行）——SSA 摘要的字段写入行
		{FunctionID: "symbol:go:example.com/m:wrap", AccessKind: domain.SummaryDirectWrite,
			FieldPath: "example.com/m.Resp.Status", LineStart: 21},
	}
	if _, err := r.SaveBatchStats(nodes, nil, sums); err != nil {
		t.Fatal(err)
	}
	return New(r), dir
}

// TestCodeSequenceDeferGoSemantics：defer/go 只改执行语义，位置不变；
// 普通调用 exec 为空。
func TestCodeSequenceDeferGoSemantics(t *testing.T) {
	acts, dir := seedSeqSemanticsRepo(t)
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "example.com/m:wrap", RepoAbs: dir, Depth: 1})
	if err != nil || root == nil {
		t.Fatalf("CodeSequence: %v root=%v", err, root)
	}
	byLine := map[int]*CodeSeqNode{}
	for _, n := range root.Nodes {
		byLine[n.Line] = n
	}
	// 17 行：unlock()（同步）；18 行：defer unlock()；19 行：go handle()
	if n := byLine[17]; n == nil || n.Exec != "" {
		t.Errorf("普通调用不应带 exec（同步语义）: %+v", n)
	}
	if n := byLine[18]; n == nil || n.Exec != SeqExecDefer || n.Label != "unlock" {
		t.Errorf("defer 调用应标 exec=defer 且 Label 保持调用名: %+v", n)
	}
	if n := byLine[19]; n == nil || n.Exec != SeqExecAsync || n.Label != "handle" {
		t.Errorf("go 调用应标 exec=async 且 Label 保持调用名: %+v", n)
	}
}

// TestCodeSequenceReturnNodeAlways：return 带调用时也有独立返回节点；返回
// 表达式内调用保序在前（InReturn）；返回后的语句不属于该路径。
func TestCodeSequenceReturnNodeAlways(t *testing.T) {
	acts, dir := seedSeqSemanticsRepo(t)
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "example.com/m:early", RepoAbs: dir, Depth: 1})
	if err != nil || root == nil {
		t.Fatalf("CodeSequence: %v root=%v", err, root)
	}
	var sawCall, sawReturn bool
	for i, n := range root.Nodes {
		switch n.Kind {
		case "call":
			if n.Label == "call" && n.InReturn {
				sawCall = true
				if i+1 >= len(root.Nodes) || root.Nodes[i+1].Kind != "return" {
					t.Errorf("return 表达式内的调用应在 return 节点之前: %+v", root.Nodes)
				}
			}
		case "return":
			sawReturn = true
			if !n.Terminates {
				t.Errorf("return 节点应标 terminates: %+v", n)
			}
			if len(n.Values) != 1 || n.Values[0] != "call()" {
				t.Errorf("return 节点应携带返回表达式文本: %+v", n)
			}
			if len(n.Slots) != 1 || n.Slots[0] != "#0" {
				t.Errorf("无具名结果时槽名应为 #序号: %+v", n)
			}
		}
	}
	if !sawCall || !sawReturn {
		t.Fatalf("应同时出现 return 表达式内的调用与 return 节点: %+v", root.Nodes)
	}

	// 返回之后的语句不在该路径上
	dead, err := acts.CodeSequence(CodeSequenceRequest{Target: "example.com/m:deadCode", RepoAbs: dir, Depth: 1})
	if err != nil || dead == nil {
		t.Fatalf("deadCode: %v root=%v", err, dead)
	}
	for _, n := range dead.Nodes {
		if n.Kind == "call" && n.Label == "audit" {
			t.Errorf("return 之后的语句不应出现在该路径上: %+v", dead.Nodes)
		}
	}
}

// TestCodeSequenceResultSlots：具名结果按位映射槽名（return 节点 slots）。
func TestCodeSequenceResultSlots(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package m

func named() (resp string, err error) {
	resp = "x"
	return resp, nil
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:named", Kind: domain.KindFunction, Name: "named", FilePath: "main.go", LineStart: 3},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	root, err := New(r).CodeSequence(CodeSequenceRequest{Target: "example.com/m:named", RepoAbs: dir, Depth: 1})
	if err != nil || root == nil {
		t.Fatalf("CodeSequence: %v root=%v", err, root)
	}
	var ret *CodeSeqNode
	for _, n := range root.Nodes {
		if n.Kind == "return" {
			ret = n
		}
	}
	if ret == nil {
		t.Fatalf("应输出 return 节点: %+v", root.Nodes)
	}
	if len(ret.Slots) != 2 || ret.Slots[0] != "resp" || ret.Slots[1] != "err" {
		t.Errorf("具名结果槽映射错: %+v", ret.Slots)
	}
	if len(ret.Values) != 2 || ret.Values[0] != "resp" || ret.Values[1] != "nil" {
		t.Errorf("返回表达式文本错: %+v", ret.Values)
	}
}

// TestCodeSequenceFocusField：--focus 字段——只保留该字段写入与相关调用，
// 无关赋值/调用被剔除；meta.focus 标 applied。
func TestCodeSequenceFocusField(t *testing.T) {
	acts, dir := seedSeqSemanticsRepo(t)
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "example.com/m:wrap", RepoAbs: dir, Depth: 1, Focus: []string{"Resp.Status"},
	})
	if err != nil || root == nil {
		t.Fatalf("CodeSequence: %v root=%v", err, root)
	}
	if root.Meta == nil || root.Meta.Focus == nil || !root.Meta.Focus.Applied {
		t.Fatalf("焦点过滤应生效并记录 meta.focus: %+v", root.Meta)
	}
	var labels []string
	for _, n := range root.Nodes {
		labels = append(labels, n.Kind+":"+n.Label)
		if n.Kind == "call" && n.Label == "audit" {
			t.Errorf("无关调用不应出现（--focus Resp.Status）: %+v", labels)
		}
	}
	var sawFocusAssign bool
	for _, n := range root.Nodes {
		if n.Kind == "assign" && n.Focus {
			sawFocusAssign = true
		}
	}
	if !sawFocusAssign {
		t.Errorf("状态字段赋值应保留并标 focus: %+v", labels)
	}
}

// TestCodeSequenceFocusReturn：--focus return——只保留 return 节点与返回
// 表达式内调用。
func TestCodeSequenceFocusReturn(t *testing.T) {
	acts, dir := seedSeqSemanticsRepo(t)
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "example.com/m:early", RepoAbs: dir, Depth: 1, Focus: []string{"return"},
	})
	if err != nil || root == nil {
		t.Fatalf("CodeSequence: %v root=%v", err, root)
	}
	if root.Meta == nil || root.Meta.Focus == nil || !root.Meta.Focus.Applied {
		t.Fatalf("焦点过滤应生效: %+v", root.Meta)
	}
	for _, n := range root.Nodes {
		if n.Kind == "assign" {
			t.Errorf("--focus return 不应输出无关赋值: %+v", n)
		}
	}
	var kinds []string
	for _, n := range root.Nodes {
		kinds = append(kinds, n.Kind)
	}
	if len(kinds) != 2 || kinds[0] != "call" || kinds[1] != "return" {
		t.Errorf("--focus return 应保留 [返回表达式内调用, return]: %v", kinds)
	}
}

// TestCodeSequenceFocusMiss：todo12-9——不存在的焦点必须明确报错（不再
// 静默保留全量输出）。合法但无写入的焦点由 focus 闭包测试单独覆盖。
func TestCodeSequenceFocusMiss(t *testing.T) {
	acts, dir := seedSeqSemanticsRepo(t)
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "example.com/m:wrap", RepoAbs: dir, Depth: 1, Focus: []string{"NoSuch.Field"},
	})
	if err == nil || root != nil {
		t.Fatalf("不存在的焦点应报错，root=%v", root)
	}
	if !strings.Contains(err.Error(), "不存在") {
		t.Errorf("错误应说明焦点不存在: %v", err)
	}
}
