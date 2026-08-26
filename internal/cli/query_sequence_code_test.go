package cli

// R81/R95 测试：query sequence --code——cli 转发（AST 解析核心断言在
// action 包）+ mermaid 渲染（renderCodeSeqMermaid 留 cli）+ 端到端。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
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

// TestCodeSequenceMermaid：mermaid 渲染（解析走 Actions.CodeSequence）——
// 消息线写调用名 + alt/loop 块。
func TestCodeSequenceMermaid(t *testing.T) {
	dir := seedCodeSeqRepo(t)
	acts, err := newTestActions(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	root, err := acts.CodeSequence(action.CodeSequenceRequest{Target: "Prepare", RepoAbs: dir, Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	m := renderCodeSeqMermaid(root)
	for _, want := range []string{
		"sequenceDiagram",
		"participant", "as Prepare",
		"->>P2: svc.LoadItems", // 消息线 = 调用名（源码调用形态）
		"alt cart == nil",
		"->>P1: ErrEmpty",
		"loop range items",
		"->>P2: svc.Validate", // 参与者 = 对象（svc 合并为 P2）
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

// seedNestedSeqRepo fixture：Prepare → svc.LoadItems → helper（嵌套
// mermaid 渲染——From 切换为被调者）。
func seedNestedSeqRepo(t *testing.T) string {
	t.Helper()
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
	return dir
}

// TestCodeSequenceNestedMermaid：R81——depth 2 嵌套渲染（From 切换为
// 被调者；P1->>P2: helper）。
func TestCodeSequenceNestedMermaid(t *testing.T) {
	dir := seedNestedSeqRepo(t)
	acts, err := newTestActions(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	root, err := acts.CodeSequence(action.CodeSequenceRequest{Target: "Prepare", RepoAbs: dir, Depth: 2})
	if err != nil {
		t.Fatal(err)
	}
	m := renderCodeSeqMermaid(root)
	if !strings.Contains(m, "P1->>P2: helper") {
		t.Errorf("mermaid 应含嵌套消息（From=LoadItems）:\n%s", m)
	}
}
