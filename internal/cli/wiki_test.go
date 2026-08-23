package cli

// #238 wiki 命令：fixture 仓库 → wiki 生成——index + 模块页六区块
// （职责/入口/核心符号/相关表）+ yaml 配置合并（描述/别名/隐藏符号）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedWikiRepo 建 fixture 仓库：包节点（doc_comment）+ 符号 + calls 边 +
// gorm 表列虚拟节点。
func seedWikiRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	pkg := &domain.CodeEntity{ID: "symbol:go:example.com/m:m", Kind: domain.KindPackage, Name: "m",
		Properties: map[string]any{"doc_comment": "主包：业务入口"}}
	main := &domain.CodeEntity{ID: "symbol:go:example.com/m:main", Kind: domain.KindFunction, Name: "main", FilePath: "main.go", LineStart: 3}
	run := &domain.CodeEntity{ID: "symbol:go:example.com/m/svc:(Svc).Run", Kind: domain.KindMethod, Name: "(Svc).Run", FilePath: "svc/svc.go", LineStart: 5}
	f1 := &domain.CodeEntity{ID: "symbol:go:example.com/m/util:F1", Kind: domain.KindFunction, Name: "F1", FilePath: "util/util.go", LineStart: 2}
	f2 := &domain.CodeEntity{ID: "symbol:go:example.com/m/util:F2", Kind: domain.KindFunction, Name: "F2", FilePath: "util/util.go", LineStart: 9}
	// gorm 表列虚拟节点（orders 表）
	col := &domain.CodeEntity{ID: "symbol:go:example.com/m:main#ext.gorm.orders.id.write@6", Kind: domain.KindFieldAccess, Name: "orders.id",
		FilePath: "main.go", LineStart: 6,
		Properties: map[string]any{"is_external": "true", "type_string": "gorm", "func_id": "symbol:go:example.com/m:main"}}
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{pkg, main, run, f1, f2, col}, nil, nil); err != nil {
		t.Fatal(err)
	}
	// calls：main 被 f1/f2 调用（callers=2 第一），main→(Svc).Run
	if _, err := r.SaveBatchStats(nil, []*domain.Fact{
		{SourceID: "symbol:go:example.com/m/util:F1", TargetID: "symbol:go:example.com/m:main", Kind: domain.FactCalls, Confidence: 0.9},
		{SourceID: "symbol:go:example.com/m/util:F2", TargetID: "symbol:go:example.com/m:main", Kind: domain.FactCalls, Confidence: 0.9},
		{SourceID: "symbol:go:example.com/m:main", TargetID: "symbol:go:example.com/m/svc:(Svc).Run", Kind: domain.FactCalls, Confidence: 0.9},
	}, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestWikiGenerate：纯自动生成——index + 模块页（职责/核心符号/相关表）。
func TestWikiGenerate(t *testing.T) {
	dir := seedWikiRepo(t)
	out := filepath.Join(t.TempDir(), "wiki")
	if code := cmdWiki([]string{"--repo", dir, "--out", out}); code != 0 {
		t.Fatalf("cmdWiki exit = %d", code)
	}
	idx, err := os.ReadFile(filepath.Join(out, "index.md"))
	if err != nil {
		t.Fatalf("index.md 未生成: %v", err)
	}
	s := string(idx)
	for _, want := range []string{"example.com/m", "m.md", "tables.md"} {
		if !strings.Contains(s, want) {
			t.Errorf("index 应含 %q:\n%s", want, s)
		}
	}
	// 表清单页含 orders
	tb, err := os.ReadFile(filepath.Join(out, "tables.md"))
	if err != nil {
		t.Fatalf("tables.md 未生成: %v", err)
	}
	if !strings.Contains(string(tb), "orders") {
		t.Errorf("tables.md 应含 orders:\n%s", tb)
	}
	mod, err := os.ReadFile(filepath.Join(out, "m.md"))
	if err != nil {
		t.Fatalf("模块页未生成: %v", err)
	}
	ms := string(mod)
	for _, want := range []string{"主包：业务入口", "main", "(Svc).Run", "orders", "F1"} {
		if !strings.Contains(ms, want) {
			t.Errorf("模块页应含 %q:\n%s", want, ms)
		}
	}
	// 核心符号排序：main（callers=2）应在 (Svc).Run 前
	iMain := strings.Index(ms, "main")
	iRun := strings.Index(ms, "(Svc).Run")
	if iMain < 0 || iRun < 0 || iMain > iRun {
		t.Errorf("核心符号应按 callers 排序（main 在前）: %d vs %d", iMain, iRun)
	}
}

// TestWikiYAML：配置合并——描述覆盖/表别名/隐藏符号。
func TestWikiYAML(t *testing.T) {
	dir := seedWikiRepo(t)
	out := filepath.Join(t.TempDir(), "wiki")
	yamlPath := filepath.Join(t.TempDir(), "wiki.yaml")
	yaml := `project:
  description: 示例业务系统
modules:
  - name: example.com/m
    description: 核心结算域
tables:
  - name: orders
    alias: 订单表
hidden_symbols:
  - F1
`
	if err := os.WriteFile(yamlPath, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := cmdWiki([]string{"--repo", dir, "--out", out, "--yaml", yamlPath}); code != 0 {
		t.Fatalf("cmdWiki exit = %d", code)
	}
	idx, err := os.ReadFile(filepath.Join(out, "index.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(idx)
	for _, want := range []string{"示例业务系统", "核心结算域"} {
		if !strings.Contains(s, want) {
			t.Errorf("index 应含 %q:\n%s", want, s)
		}
	}
	// 表别名在 tables.md
	tb, err := os.ReadFile(filepath.Join(out, "tables.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(tb), "订单表") {
		t.Errorf("tables.md 应含表别名 订单表:\n%s", tb)
	}
	mod, err := os.ReadFile(filepath.Join(out, "m.md"))
	if err != nil {
		t.Fatal(err)
	}
	ms := string(mod)
	if strings.Contains(ms, "F1") {
		t.Errorf("隐藏符号 F1 不应出现:\n%s", ms)
	}
}
