package action

// R95 测试（迁自 cli/query_sequence_code_test.go + query_r84_test.go）：
// Actions.CodeSequence——AST 解析（调用/分支/循环/嵌套展开/接口方法
// 入口具体化/停止包）。渲染（mermaid/文本）留 cli 测试。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedSeqCodeRepo fixture：真实源码文件 + 索引节点（FilePath 指向源码）。
func seedSeqCodeRepo(t *testing.T) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package m

import "example.com/m/svc"

func Prepare(cart *Cart) error {
	if cart == nil {
		return ErrEmpty()
	}
	items := svc.LoadItems(cart.Code)
	for _, it := range items {
		svc.Validate(it)
	}
	svc.Save(cart.Code)
	return nil
}

func Cart() {}
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
		{ID: "symbol:go:example.com/m:Prepare", Kind: domain.KindFunction, Name: "Prepare", FilePath: "main.go", LineStart: 5},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	return New(r), dir
}

// TestCodeSequenceNodes：AST 解析——调用/分支/循环节点齐全 + 顺序。
func TestCodeSequenceNodes(t *testing.T) {
	acts, dir := seedSeqCodeRepo(t)
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Prepare", RepoAbs: dir, Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if root == nil {
		t.Fatal("CodeSequence 返回 nil")
	}
	if root.Label != "Prepare" {
		t.Errorf("入口 = %q; want Prepare", root.Label)
	}
	// 语句：if → LoadItems 赋值 → for → Save → return（S2 裸 return 节点）
	if len(root.Nodes) != 5 {
		t.Fatalf("顶层步骤 = %d; want 5（if/赋值/for/调用/return）:\n%+v", len(root.Nodes), root.Nodes)
	}
	if root.Nodes[0].Kind != "branch" || !strings.Contains(root.Nodes[0].Label, "cart == nil") {
		t.Errorf("步骤 1 应为 if 分支: %+v", root.Nodes[0])
	}
	if root.Nodes[1].Kind != "call" || root.Nodes[1].Label != "svc.LoadItems" {
		t.Errorf("步骤 2 应为 svc.LoadItems 调用: %+v", root.Nodes[1])
	}
	if root.Nodes[2].Kind != "loop" || !strings.Contains(root.Nodes[2].Label, "range items") {
		t.Errorf("步骤 3 应为 range 循环: %+v", root.Nodes[2])
	}
	if root.Nodes[3].Kind != "call" || root.Nodes[3].Label != "svc.Save" {
		t.Errorf("步骤 4 应为 svc.Save 调用: %+v", root.Nodes[3])
	}
	// 分支内调用 + else
	if len(root.Nodes[0].Nodes) != 1 || root.Nodes[0].Nodes[0].Label != "ErrEmpty" {
		t.Errorf("if 分支内应有调用: %+v", root.Nodes[0])
	}
	// 循环体内调用
	if len(root.Nodes[2].Nodes) != 1 || root.Nodes[2].Nodes[0].Label != "svc.Validate" {
		t.Errorf("循环体内应有调用: %+v", root.Nodes[2])
	}
}

// seedNestedSeqRepo fixture：Prepare → svc.LoadItems → helper（depth 2
// 嵌套展开用）。
func seedNestedSeqRepo(t *testing.T) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package m

import "example.com/m/svc"

func Prepare() {
	svc.LoadItems()
}
`
	svcSrc := `package svc

func LoadItems() {
	helper()
}

func helper() {}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "svc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "svc", "svc.go"), []byte(svcSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:Prepare", Kind: domain.KindFunction, Name: "Prepare", FilePath: "main.go", LineStart: 5},
		{ID: "symbol:go:example.com/m/svc:LoadItems", Kind: domain.KindFunction, Name: "LoadItems", FilePath: "svc/svc.go", LineStart: 4},
		{ID: "symbol:go:example.com/m/svc:helper", Kind: domain.KindFunction, Name: "helper", FilePath: "svc/svc.go", LineStart: 8},
	}, []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:Prepare", TargetID: "symbol:go:example.com/m/svc:LoadItems",
			Kind: domain.FactCalls, Confidence: 0.9, Metadata: map[string]any{"line_num": 6}},
		{SourceID: "symbol:go:example.com/m/svc:LoadItems", TargetID: "symbol:go:example.com/m/svc:helper",
			Kind: domain.FactCalls, Confidence: 0.9, Metadata: map[string]any{"line_num": 5}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	return New(r), dir
}

// TestCodeSequenceNested：R81——--depth 2 嵌套展开（被调函数内部调用
// 递归解析——行号对齐索引调用边；depth 递减）。
func TestCodeSequenceNested(t *testing.T) {
	acts, dir := seedNestedSeqRepo(t)
	// depth 1：LoadItems 无嵌套
	root1, err := acts.CodeSequence(CodeSequenceRequest{Target: "Prepare", RepoAbs: dir, Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(root1.Nodes) != 1 || len(root1.Nodes[0].Nodes) != 0 {
		t.Fatalf("depth 1 不应嵌套展开: %+v", root1.Nodes)
	}
	// depth 2：LoadItems 展开 helper
	root2, err := acts.CodeSequence(CodeSequenceRequest{Target: "Prepare", RepoAbs: dir, Depth: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(root2.Nodes) != 1 {
		t.Fatalf("depth 2 顶层步骤 = %d; want 1", len(root2.Nodes))
	}
	nested := root2.Nodes[0]
	if nested.Kind != "call" || nested.Label != "svc.LoadItems" || len(nested.Nodes) != 1 {
		t.Fatalf("depth 2 应嵌套展开 LoadItems: %+v", nested)
	}
	if nested.Nodes[0].Label != "helper" {
		t.Errorf("嵌套内调用 = %q; want helper", nested.Nodes[0].Label)
	}
}

// TestCodeSequenceStopPkg：R83——停止包命中 → depth 2 不展开内部
// （节点保留 Nodes 空；StopPackages 由 cli 传入）。
func TestCodeSequenceStopPkg(t *testing.T) {
	acts, dir := seedNestedSeqRepo(t)
	// 无停止包：depth 2 展开 helper
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Prepare", RepoAbs: dir, Depth: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(root.Nodes) != 1 || len(root.Nodes[0].Nodes) != 1 {
		t.Fatalf("无停止包 depth 2 应展开: %+v", root.Nodes)
	}
	// svc 在停止列表：depth 2 不展开（节点保留 Nodes 空）
	root2, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "Prepare", RepoAbs: dir, Depth: 2, StopPackages: []string{"example.com/m/svc"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(root2.Nodes) != 1 || len(root2.Nodes[0].Nodes) != 0 {
		t.Fatalf("停止包应不展开内部: %+v", root2.Nodes)
	}
	if root2.Nodes[0].Label != "svc.LoadItems" {
		t.Errorf("节点应保留（不深入）: %+v", root2.Nodes[0])
	}
}

// seedIfaceEntryRepo fixture（R84）：接口方法（无方法体——grpc 服务
// 入口形态）+ implements 边 → 实现方法（有方法体，内部调用 helper）。
func seedIfaceEntryRepo(t *testing.T) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package m

type Svc interface {
	Run() error
}

type svcImpl struct{}

func (s *svcImpl) Run() error {
	return helper()
}

func helper() error { return nil }
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
		// 接口 + 接口方法（无方法体——动态入口）
		{ID: "symbol:go:example.com/m:Svc", Kind: domain.KindInterface, Name: "Svc", FilePath: "main.go", LineStart: 3},
		{ID: "symbol:go:example.com/m:(Svc).Run", Kind: domain.KindMethod, Name: "(Svc).Run", FilePath: "main.go", LineStart: 4},
		// 实现类型 + 实现方法（有方法体）
		{ID: "symbol:go:example.com/m:svcImpl", Kind: domain.KindStruct, Name: "svcImpl", FilePath: "main.go", LineStart: 8},
		{ID: "symbol:go:example.com/m:(svcImpl).Run", Kind: domain.KindMethod, Name: "(svcImpl).Run", FilePath: "main.go", LineStart: 10},
		{ID: "symbol:go:example.com/m:helper", Kind: domain.KindFunction, Name: "helper", FilePath: "main.go", LineStart: 14},
	}, []*domain.Fact{
		// 接口 → 实现
		{SourceID: "symbol:go:example.com/m:Svc", TargetID: "symbol:go:example.com/m:svcImpl",
			Kind: domain.FactImplements, Confidence: 1.0},
		// 实现方法内部调用 helper
		{SourceID: "symbol:go:example.com/m:(svcImpl).Run", TargetID: "symbol:go:example.com/m:helper",
			Kind: domain.FactCalls, Confidence: 1.0, Metadata: map[string]any{"line_num": 11}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	return New(r), dir
}

// TestCodeSequenceIfaceEntry：接口方法作为查询入口（动态入口无方法体）
// → 具体化到实现方法后展开其内部调用。
func TestCodeSequenceIfaceEntry(t *testing.T) {
	acts, dir := seedIfaceEntryRepo(t)
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/m:(Svc).Run", RepoAbs: dir, Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if root == nil {
		t.Fatal("CodeSequence 接口方法入口应具体化到实现方法（nil 返回）")
	}
	// 实现方法内部调用了 helper——展开后应出现
	found := false
	var walk func(n *CodeSeqNode)
	walk = func(n *CodeSeqNode) {
		if strings.Contains(n.Label, "helper") {
			found = true
		}
		for _, c := range n.Nodes {
			walk(c)
		}
		if n.Else != nil {
			for _, c := range n.Else {
				walk(c)
			}
		}
	}
	walk(root)
	if !found {
		t.Errorf("接口方法入口应展开实现方法内部调用（helper 缺失）:\n%+v", root)
	}
}

// TestCodeSequenceCycleGuard：递归自环（函数调自身）——路径防环——
// 嵌套展开不无限深入（visited 路径语义；depth 5 也只展开一层）。
