package cli

// R81 测试：query sequence --code 代码级时序——AST 解析函数体
// （调用名/分支/循环）+ mermaid alt/loop 渲染。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedCodeSeqRepo fixture：真实源码文件 + 索引节点（FilePath 指向源码）。
func seedCodeSeqRepo(t *testing.T) string {
	t.Helper()
	dir := seedRepo(t)
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
	defer db.Close()
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:Prepare", Kind: domain.KindFunction, Name: "Prepare", FilePath: "main.go", LineStart: 5},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestCodeSequenceNodes：AST 解析——调用/分支/循环节点齐全 + 顺序。
func TestCodeSequenceNodes(t *testing.T) {
	dir := seedCodeSeqRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	acts, err := newTestActions(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	root := codeSequence(acts, dir, "Prepare", 1)
	if root == nil {
		t.Fatal("codeSequence 返回 nil")
	}
	if root.Label != "Prepare" {
		t.Errorf("入口 = %q; want Prepare", root.Label)
	}
	// 语句：if → LoadItems 赋值 → for → Save
	if len(root.Nodes) != 4 {
		t.Fatalf("顶层步骤 = %d; want 4（if/赋值/for/调用）:\n%+v", len(root.Nodes), root.Nodes)
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

// TestCodeSequenceMermaid：mermaid 渲染——消息线写调用名 + alt/loop 块。
func TestCodeSequenceMermaid(t *testing.T) {
	dir := seedCodeSeqRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	acts, err := newTestActions(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	root := codeSequence(acts, dir, "Prepare", 1)
	m := renderCodeSeqMermaid(root)
	for _, want := range []string{
		"sequenceDiagram",
		"participant", "as Prepare",
		"->>P2: svc.LoadItems", // 消息线 = 调用名（源码调用形态）
		"alt cart == nil",
		"->>P1: ErrEmpty",
		"loop range items",
		"->>P4: svc.Validate",
	} {
		if !strings.Contains(m, want) {
			t.Errorf("mermaid 应含 %q:\n%s", want, m)
		}
	}
}

// TestCodeSequenceCmd：命令端到端（--code --format mermaid）。
func TestCodeSequenceCmd(t *testing.T) {
	dir := seedCodeSeqRepo(t)
	out := captureStdout(func() {
		if code := cmdQuery([]string{"sequence", "--code", "Prepare", "--repo", dir, "--format", "mermaid"}); code != 0 {
			t.Fatalf("sequence --code exit = %d", code)
		}
	})
	for _, want := range []string{"sequenceDiagram", "svc.LoadItems", "alt", "loop"} {
		if !strings.Contains(out, want) {
			t.Errorf("sequence --code 应含 %q:\n%s", want, out)
		}
	}
}

// TestCodeSequenceNested：R81——--depth 2 嵌套展开（被调函数内部调用
// 递归解析——行号对齐索引调用边；From 切换为被调者）。
func TestCodeSequenceNested(t *testing.T) {
	dir := seedRepo(t)
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
	defer db.Close()
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
	acts, err := newTestActions(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	// depth 1：LoadItems 无嵌套
	root1 := codeSequence(acts, dir, "Prepare", 1)
	if len(root1.Nodes) != 1 || len(root1.Nodes[0].Nodes) != 0 {
		t.Fatalf("depth 1 不应嵌套展开: %+v", root1.Nodes)
	}
	// depth 2：LoadItems 展开 helper
	root2 := codeSequence(acts, dir, "Prepare", 2)
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
	// mermaid：嵌套消息 From 切换为 LoadItems（P2->>P1: helper）
	m := renderCodeSeqMermaid(root2)
	if !strings.Contains(m, "P2->>P1: helper") {
		t.Errorf("mermaid 应含嵌套消息（From=LoadItems）:\n%s", m)
	}
}

// TestCodeSequenceMissing：源码缺失 → 报错非零（提示 fallback）。
func TestCodeSequenceMissing(t *testing.T) {
	dir := seedRepo(t) // 无 Prepare 符号
	code := cmdQuery([]string{"sequence", "--code", "nope", "--repo", dir})
	if code == 0 {
		t.Error("符号不存在应非零退出")
	}
}
