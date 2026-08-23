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
	// Q251-A：模块页架构图区块 = 包间调用图（util→m x2、m→svc x1）
	for _, want := range []string{"包间调用", "util[util] -->|2| m[m]", "m[m] -->|1| svc[svc]"} {
		if !strings.Contains(ms, want) {
			t.Errorf("模块页包间调用图应含 %q:\n%s", want, ms)
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








// TestWikiAutoDesc：F——模块描述自动推断 fallback（yaml/包注释都空
// 时只陈述代码事实，不编造业务含义）。
func TestWikiAutoDesc(t *testing.T) {
	wm := &domain.WikiModule{
		Name: "x",
		CoreSymbols: []*domain.WikiSymbol{
			{Name: "Run", Kind: "method", Callers: 5},
			{Name: "Init", Kind: "func", Callers: 3},
			{Name: "Close", Kind: "func", Callers: 1},
			{Name: "Extra", Kind: "func", Callers: 0},
		},
		OutCalls: []string{"a", "b"},
		InCalls:  []string{"c"},
	}
	got := wikiAutoDesc(wm)
	for _, want := range []string{"自动推断", "核心符号 Run、Init、Close", "调用 2 个模块", "被 1 个模块调用"} {
		if !strings.Contains(got, want) {
			t.Errorf("wikiAutoDesc 应含 %q: %s", want, got)
		}
	}
	// 无数据：空（不显示"无描述"以外的提示）
	if wikiAutoDesc(&domain.WikiModule{Name: "empty"}) != "" {
		t.Errorf("空模块自动推断应为空")
	}
}

// TestArchMermaidFallback：R2——yaml architecture 空时自动包间调用
// 聚合图（同 from→to 计数相加，确定性排序）。
func TestArchMermaidFallback(t *testing.T) {
	data := []*domain.WikiModule{
		{Name: "m1", PkgCalls: []*domain.WikiPkgCall{
			{From: "cli", To: "action", Count: 5},
			{From: "cli", To: "server", Count: 1},
		}},
		{Name: "m2", PkgCalls: []*domain.WikiPkgCall{
			{From: "cli", To: "action", Count: 3},
		}},
	}
	got := archMermaidFallback(data)
	for _, want := range []string{"cli[cli] -->|8| action[action]", "cli[cli] -->|1| server[server]"} {
		if !strings.Contains(got, want) {
			t.Errorf("fallback 应含 %q:\n%s", want, got)
		}
	}
	if archMermaidFallback(nil) != "" {
		t.Errorf("空数据 fallback 应为空")
	}
}

// TestMergeTableColumnsHidden：R3——yaml 列 hidden 同时过滤自动列
// （解析噪音列：别名列错误归属产生的表.列虚拟节点）。
func TestMergeTableColumnsHidden(t *testing.T) {
	cols := []*domain.TableColumn{
		{Name: "edges.name", ColType: "TEXT"},
		{Name: "edges.id", ColType: "INTEGER"},
	}
	yamlCols := []wikiTableColumn{
		{Name: "id", Comment: "自增主键"},
		{Name: "name", Comment: "噪音", Hidden: true},
	}
	rows := mergeTableColumns("edges", cols, yamlCols)
	for _, r := range rows {
		if r.name == "name" {
			t.Errorf("hidden 列 name 不应渲染: %+v", rows)
		}
	}
	if len(rows) != 1 || rows[0].name != "id" {
		t.Errorf("rows = %+v, want 仅 id", rows)
	}
}

// TestExtractEnums：R5——枚举提取（类型化/字符串 const + 注释 +
// 长文本过滤 + 测试文件排除）。
func TestExtractEnums(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "internal", "demo", "kinds.go"), `package demo

// EdgeKind 边类型。
type EdgeKind string

const (
	EdgeCalls  EdgeKind = "calls"
	EdgeAlias  EdgeKind = "alias" // 指针别名
	LongText            = "这是一个非常长的文本常量，用来测试长文本过滤逻辑是否正确工作，应该被过滤掉不当作枚举展示出来"
)

// 无类型常量（展示标签等）——默认过滤，--include-untyped 放开
const (
	StatusOK    = "ok"
	StatusFail  = "fail"
)
`)
	entries := extractEnums(dir, true)
	found := map[string]bool{}
	for _, e := range entries {
		found[e.Name] = true
		if e.Name == "EdgeCalls" {
			if e.Type != "EdgeKind" || e.Value != "calls" || e.Pkg != "demo" {
				t.Errorf("EdgeCalls 提取 = %+v", e)
			}
		}
		if e.Name == "EdgeAlias" && e.Comment != "指针别名" {
			t.Errorf("EdgeAlias 注释 = %q", e.Comment)
		}
	}
	if !found["EdgeCalls"] || !found["EdgeAlias"] {
		t.Errorf("应提取 EdgeCalls/EdgeAlias: %v", found)
	}
	if found["LongText"] {
		t.Errorf("长文本常量不应提取: %v", found)
	}
	// R6：默认过滤无类型常量（StatusOK 无显式类型）
	if found["StatusOK"] {
		t.Errorf("无类型常量默认应过滤: %v", found)
	}
	// --include-untyped 放开
	all := extractEnums(dir, false)
	foundAll := map[string]bool{}
	for _, e := range all {
		foundAll[e.Name] = true
	}
	if !foundAll["StatusOK"] || !foundAll["EdgeCalls"] {
		t.Errorf("include-untyped 应含无类型常量: %v", foundAll)
	}
}
