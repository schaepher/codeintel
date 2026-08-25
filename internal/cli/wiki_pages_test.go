package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

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
		"<title>",
		"id=\"sidebar\"",
		"example.com/m",
		"fold",
		"id=\"tables\"",
		"id=\"er\"",
		"href=\"#er\"",
		"无表间直接关联",
		"orders",
		"<style>",
		"<script>",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("html 应含 %q", want)
		}
	}

	body := regexp.MustCompile(`(?s)<script>.*?</script>`).ReplaceAllString(s, "")
	if strings.Contains(body, "## ") {
		t.Error("html 不应含 markdown 标题")
	}

	out2 := filepath.Join(t.TempDir(), "wiki2")
	if code := cmdWiki([]string{"--repo", dir, "--out", out2}); code != 0 {
		t.Fatalf("cmdWiki md exit = %d", code)
	}
	if _, err := os.Stat(filepath.Join(out2, "index.md")); err != nil {
		t.Error("默认 format 应生成 index.md")
	}

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
domains:
  - name: 测试域
    packages: [example.com/m]
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
	// R32：默认 plantuml——mermaid 模式显式 --diagram mermaid
	if code := cmdWiki([]string{"--repo", dir, "--out", out, "--yaml", yamlPath, "--diagram", "mermaid"}); code != 0 {
		t.Fatalf("cmdWiki exit = %d", code)
	}
	mod, err := os.ReadFile(filepath.Join(out, "m.md"))
	if err != nil {
		t.Fatal(err)
	}
	ms := string(mod)

	for _, want := range []string{"```mermaid", "sequenceDiagram", "下单流程", "graph LR"} {
		if !strings.Contains(ms, want) {
			t.Errorf("模块页应含 %q:\n%s", want, ms)
		}
	}

	if !strings.Contains(ms, `participant P0 as "main"`) || !strings.Contains(ms, "P0->>P1: call") {
		t.Errorf("应含自动时序（main 调用链）:\n%s", ms)
	}

	// R41：html mermaid 模式——script 块闭合（mermaid.initialize 块缺
	// </script> 会把主 JS（fold 逻辑）吞进同一块 → Unexpected token '<'
	// → 折叠点击失效；闭合后 mermaid.initialize 是独立 script 块
	outH := filepath.Join(t.TempDir(), "wikiH")
	if code := cmdWiki([]string{"--repo", dir, "--out", outH, "--yaml", yamlPath, "--diagram", "mermaid", "--format", "html"}); code != 0 {
		t.Fatalf("cmdWiki html exit = %d", code)
	}
	htmlB, err := os.ReadFile(filepath.Join(outH, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	hs := string(htmlB)
	// mermaid.initialize 块必须闭合（否则主 JS 并入同一 script 块）；
	// R50：startOnLoad false + renderVisibleMermaid（隐藏容器渲染 NaN 修复）
	if !strings.Contains(hs, "mermaid.initialize({ startOnLoad: false, theme: 'neutral' });") ||
		!strings.Contains(hs, "renderVisibleMermaid") {
		t.Errorf("mermaid 块应独立闭合且含延迟渲染（startOnLoad false）:\n%s", hs)
	}
	// fold JS 存在且独立：最后一个内联 script 块（主 JS）内部不应含
	// <script> 字面量（否则该块被 HTML 解析器截断——折叠失效）
	mainScriptStart := strings.LastIndex(hs, "<script>\n// 目录当前模块高亮")
	if mainScriptStart < 0 {
		t.Error("未找到主 script 块（目录高亮 IIFE）")
	} else {
		// 内容从打开标签之后开始（排除 <script> 本身；JS 里 <= / (<) /
		// '<span' 字符串是合法语法，真正致命的是 <script> 字面量——HTML
		// 解析器在 script 块内遇 </script> 提前终止）
		openTag := "<script>"
		mainScriptEnd := strings.Index(hs[mainScriptStart:], "</script>")
		mainBody := hs[mainScriptStart+len(openTag) : mainScriptStart+mainScriptEnd]
		if strings.Contains(mainBody, "<script>") || strings.Contains(mainBody, "</script>") {
			t.Error("主 script 块内不应含 script 标签字面量（折叠 JS 解析失败）")
		}
	}
}

// TestWikiPlantumlDefault：R32——默认 plantuml 模式：md 输出 ```plantuml
// 块（graph/sequence 转换），HTML 渲染 PNG base64 <img>（无 mermaid CDN）。
func TestWikiPlantumlDefault(t *testing.T) {
	dir := seedWikiRepo(t)
	out := filepath.Join(t.TempDir(), "wiki")
	if code := cmdWiki([]string{"--repo", dir, "--out", out, "--format", "html"}); code != 0 {
		t.Fatalf("cmdWiki html exit = %d", code)
	}
	html, err := os.ReadFile(filepath.Join(out, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(html)
	if strings.Contains(s, "mermaid.min.js") {
		t.Error("plantuml 模式不应引 mermaid CDN")
	}
	if !strings.Contains(s, "data:image/png;base64,") {
		t.Error("plantuml 模式 HTML 应含 PNG base64 图（ER/架构等）")
	}
	// md 版：plantuml 文本块
	out2 := filepath.Join(t.TempDir(), "wiki2")
	if code := cmdWiki([]string{"--repo", dir, "--out", out2}); code != 0 {
		t.Fatalf("cmdWiki md exit = %d", code)
	}
	idx, err := os.ReadFile(filepath.Join(out2, "index.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"```plantuml", "@startuml"} {
		if !strings.Contains(string(idx), want) {
			t.Errorf("md 应含 %q（plantuml 文本块）:\n%s", want, idx)
		}
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
domains:
  - name: 测试域
    packages: [example.com/m]
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

	mod, err := os.ReadFile(filepath.Join(out, "m.md"))
	if err != nil {
		t.Fatal(err)
	}
	ms := string(mod)
	if !strings.Contains(ms, "tables.md#orders") {
		t.Errorf("相关表应链接 tables.md#orders:\n%s", ms)
	}

	if !strings.Contains(ms, "### 内部调用链：(Svc).Run") || !strings.Contains(ms, `participant P0 as "main"`) || !strings.Contains(ms, "P0->>P1: call") {
		t.Errorf("时序应按一级调用分支单独画:\n%s", ms)
	}
}
