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
	root := codeSequence(acts, dir, "Prepare")
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
	root := codeSequence(acts, dir, "Prepare")
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

// TestCodeSequenceMissing：源码缺失 → 报错非零（提示 fallback）。
func TestCodeSequenceMissing(t *testing.T) {
	dir := seedRepo(t) // 无 Prepare 符号
	code := cmdQuery([]string{"sequence", "--code", "nope", "--repo", dir})
	if code == 0 {
		t.Error("符号不存在应非零退出")
	}
}
