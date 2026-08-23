package cli

// #238 wiki 命令：fixture 仓库 → wiki 生成——index + 模块页六区块
// （职责/入口/核心符号/相关表）+ yaml 配置合并（描述/别名/隐藏符号）。

import (
	"os"
	"path/filepath"
	"regexp"
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

// TestWikiHTML：--format html 单文件自包含——目录导航 + 折叠标记 +
// 六区块内容。
func TestWikiHTML(t *testing.T) {
	dir := seedWikiRepo(t)
	out := filepath.Join(t.TempDir(), "wiki")
	if code := cmdWiki([]string{"--repo", dir, "--out", out, "--format", "html"}); code != 0 {
		t.Fatalf("cmdWiki html exit = %d", code)
	}
	html, err := os.ReadFile(filepath.Join(out, "index.html"))
	if err != nil {
		t.Fatalf("index.html 未生成: %v", err)
	}
	s := string(html)
	for _, want := range []string{
		"<title>",           // 页面标题
		"id=\"sidebar\"",    // 左侧目录栏
		"example.com/m",     // 模块名（目录 + 内容）
		"fold",              // 折叠交互标记
		"id=\"tables\"",     // 表清单区块
		"id=\"er\"",         // Q251 ER 图区块
		"href=\"#er\"",      // 导航 ER 图入口
		"无表间直接关联",     // fixture 无关系 → 降级提示
		"orders",            // 相关表
		"<style>",           // 内嵌 CSS
		"<script>",          // 内嵌 JS
	} {
		if !strings.Contains(s, want) {
			t.Errorf("html 应含 %q", want)
		}
	}
	// Q251：内嵌 mermaid JS 源码含 "##" 字符串——剥离 script 块后
	// 检查 markdown 泄漏
	body := regexp.MustCompile(`(?s)<script>.*?</script>`).ReplaceAllString(s, "")
	if strings.Contains(body, "## ") {
		t.Error("html 不应含 markdown 标题")
	}
	// 无 --format 时默认 md（回归）
	out2 := filepath.Join(t.TempDir(), "wiki2")
	if code := cmdWiki([]string{"--repo", dir, "--out", out2}); code != 0 {
		t.Fatalf("cmdWiki md exit = %d", code)
	}
	if _, err := os.Stat(filepath.Join(out2, "index.md")); err != nil {
		t.Error("默认 format 应生成 index.md")
	}
	// Q251：md 输出应含 er.md（ER 图页面）且 index 链接到它
	if _, err := os.Stat(filepath.Join(out2, "er.md")); err != nil {
		t.Error("md 输出应生成 er.md")
	}
	idx2, err := os.ReadFile(filepath.Join(out2, "index.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(idx2), "er.md") {
		t.Error("index.md 应链接 er.md")
	}
}

// TestWikiMermaid：架构图 + 流程时序——md 输出含 mermaid 代码块
// （graph/sequenceDiagram），yaml 可覆盖架构 + 追加业务时序。
func TestWikiMermaid(t *testing.T) {
	dir := seedWikiRepo(t)
	out := filepath.Join(t.TempDir(), "wiki")
	yamlPath := filepath.Join(t.TempDir(), "wiki.yaml")
	yaml := `modules:
  - name: example.com/m
architecture: |
  graph LR
    A[用户] --> B[系统]
flows:
  - title: 下单流程
    mermaid: |
      sequenceDiagram
        User->>System: 下单
        System->>DB: 写订单
`
	if err := os.WriteFile(yamlPath, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := cmdWiki([]string{"--repo", dir, "--out", out, "--yaml", yamlPath}); code != 0 {
		t.Fatalf("cmdWiki exit = %d", code)
	}
	mod, err := os.ReadFile(filepath.Join(out, "m.md"))
	if err != nil {
		t.Fatal(err)
	}
	ms := string(mod)
	// 自动架构图（模块间调用）——fixture 单模块无跨模块边，显示提示或空
	for _, want := range []string{"```mermaid", "sequenceDiagram", "下单流程", "graph LR"} {
		if !strings.Contains(ms, want) {
			t.Errorf("模块页应含 %q:\n%s", want, ms)
		}
	}
	// 自动时序：核心符号第一名（main）的调用链
	if !strings.Contains(ms, "main->>") && !strings.Contains(ms, "main->") {
		t.Errorf("应含自动时序（main 调用链）:\n%s", ms)
	}
}

// TestWikiTableDetail：#243 表详情——字段定义表/索引/建表语句 + 时序
// 逐流程单独画。
func TestWikiTableDetail(t *testing.T) {
	dir := seedWikiRepo(t)
	out := filepath.Join(t.TempDir(), "wiki")
	yamlPath := filepath.Join(t.TempDir(), "wiki.yaml")
	yaml := `modules:
  - name: example.com/m
tables:
  - name: orders
    alias: 订单表
    columns:
      - name: id
        type: bigint
        comment: 主键
      - name: user_id
        type: bigint
        default: "0"
        comment: 用户
    indexes:
      - idx_orders_user(user_id)
    ddl: |
      CREATE TABLE orders (id bigint PRIMARY KEY, user_id bigint)
`
	if err := os.WriteFile(yamlPath, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := cmdWiki([]string{"--repo", dir, "--out", out, "--yaml", yamlPath}); code != 0 {
		t.Fatalf("cmdWiki exit = %d", code)
	}
	tb, err := os.ReadFile(filepath.Join(out, "tables.md"))
	if err != nil {
		t.Fatal(err)
	}
	ts := string(tb)
	for _, want := range []string{"## orders", "字段名", "类型", "默认值", "说明", "id", "bigint", "主键", "idx_orders_user", "CREATE TABLE orders", "```sql"} {
		if !strings.Contains(ts, want) {
			t.Errorf("tables.md 应含 %q:\n%s", want, ts)
		}
	}
	// 模块页：相关表链接 tables.md 锚点 + 时序逐流程（main→(Svc).Run 独立图）
	mod, err := os.ReadFile(filepath.Join(out, "m.md"))
	if err != nil {
		t.Fatal(err)
	}
	ms := string(mod)
	if !strings.Contains(ms, "tables.md#orders") {
		t.Errorf("相关表应链接 tables.md#orders:\n%s", ms)
	}
	// 自动时序：main 的一级 callee = (Svc).Run → 单独一张图
	if !strings.Contains(ms, "### 内部调用链：(Svc).Run") || !strings.Contains(ms, "main->>(Svc).Run") {
		t.Errorf("时序应按一级调用分支单独画:\n%s", ms)
	}
}

// TestWikiERMermaid：Q251 ER 图页面——erDiagram 实体/关系行渲染，
// fk/query 画线、write/read 剔除、隐藏表过滤、列级 label、确定性排序。
func TestWikiERMermaid(t *testing.T) {
	rels := []*domain.TableRelation{
		{FromTable: "orders", FromCol: "user_id", ToTable: "users", ToCol: "id", Type: domain.RelationFK, Hops: 1},
		{FromTable: "users", FromCol: "id", ToTable: "orders", ToCol: "user_id", Type: domain.RelationQuery, Hops: 2},
		{FromTable: "orders", FromCol: "total", ToTable: "audit", ToCol: "amount", Type: domain.RelationWrite, Hops: 1},
		{FromTable: "secret", FromCol: "x", ToTable: "users", ToCol: "id", Type: domain.RelationQuery, Hops: 3},
	}
	hide := map[string]bool{"secret": true}
	m := renderERMermaid(rels, hide)
	if !strings.HasPrefix(m, "erDiagram\n") {
		t.Errorf("应以 erDiagram 开头:\n%s", m)
	}
	for _, want := range []string{"    orders\n", "    users\n", "user_id → id [fk]", "id → user_id [query]"} {
		if !strings.Contains(m, want) {
			t.Errorf("ER 图应含 %q:\n%s", want, m)
		}
	}
	for _, bad := range []string{"secret", "write", "audit"} {
		if strings.Contains(m, bad) {
			t.Errorf("ER 图不应含 %q（write/隐藏表剔除）:\n%s", bad, m)
		}
	}
	// 确定性：同输入两次输出一致
	if m2 := renderERMermaid(rels, hide); m2 != m {
		t.Errorf("ER 图输出不确定")
	}
	// 空关系
	if e := renderERMermaid(nil, nil); e != "erDiagram\n" {
		t.Errorf("空关系应返回裸 erDiagram，got %q", e)
	}
}

// TestWikiERPage：Q251 er.md 页面——erDiagram 代码块 + 关系明细表。
func TestWikiERPage(t *testing.T) {
	rels := []*domain.TableRelation{
		{FromTable: "orders", FromCol: "user_id", ToTable: "users", ToCol: "id", Type: domain.RelationFK, Hops: 1},
	}
	page := renderERPage(rels, nil)
	for _, want := range []string{"# ER 图", "```mermaid", "erDiagram", "| orders | user_id | users | id | fk |", "tables.md"} {
		if !strings.Contains(page, want) {
			t.Errorf("er.md 应含 %q:\n%s", want, page)
		}
	}
	// 无关联提示
	if p2 := renderERPage(nil, nil); !strings.Contains(p2, "无表间直接关联") {
		t.Errorf("空关系应提示无关联:\n%s", p2)
	}
}
