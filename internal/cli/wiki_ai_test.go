package cli

// #0 wiki --ai 增量补缺：缺口收集 → AI 初稿 → 合并 wiki.yaml
// （保留注释、AI 初稿标注、失败重试一次）。测试先行。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
	"gopkg.in/yaml.v3"
)

// aiFixtureData 构造缺口数据：1 个模块无描述、1 张表无别名、1 个列无说明。
func aiFixtureData() ([]*domain.WikiModule, wikiConfig, []*domain.TableColumn) {
	data := []*domain.WikiModule{
		{Name: "example.com/app", ShortName: "app", Desc: "业务入口",
			CoreSymbols: []*domain.WikiSymbol{{Name: "main", Callers: 0}}},
		{Name: "example.com/app/internal/agent", ShortName: "agent", Desc: "",
			CoreSymbols: []*domain.WikiSymbol{{Name: "(Manager).Run", Callers: 12}}},
	}
	cfg := wikiConfig{}
	cfg.Modules = []wikiModuleCfg{{Name: "example.com/app", Description: "业务入口（人工）"}}
	cfg.Tables = []wikiTableConfig{
		{Name: "order_tab", Alias: "订单表"},
		{Name: "user_tab"},
	}
	cols := []*domain.TableColumn{
		{Name: "order_tab.id", ColType: "INTEGER"},
		{Name: "order_tab.order_no"},
		{Name: "user_tab.id", ColType: "INTEGER"},
	}
	return data, cfg, cols
}

// TestWikiAICollectGaps：缺口收集——有内容的跳过、缺的进列表。
func TestWikiAICollectGaps(t *testing.T) {
	data, cfg, cols := aiFixtureData()
	mods, tbls, colGaps := wikiAIGaps(data, cfg, cols)
	if len(mods) != 1 || mods[0].name != "example.com/app/internal/agent" {
		t.Errorf("模块缺口 = %+v; want 仅 agent（app 有描述跳过）", mods)
	}
	if len(tbls) != 1 || tbls[0].name != "user_tab" {
		t.Errorf("表缺口 = %+v; want 仅 user_tab（order_tab 有别名跳过）", tbls)
	}
	// 列缺口：两张表都有列无 comment（类型不算说明，也计入）
	byTbl := map[string][]string{}
	for _, g := range colGaps {
		byTbl[g.table] = g.cols
	}
	if len(colGaps) != 2 || len(byTbl["order_tab"]) != 2 || len(byTbl["user_tab"]) != 1 {
		t.Errorf("列缺口 = %+v; want order_tab[id,order_no] user_tab[id]", colGaps)
	}
}

// TestWikiAIStripFence：AI 输出 yaml 围栏剥离（```yaml / ``` 变体）。
func TestWikiAIStripFence(t *testing.T) {
	cases := []struct{ in, want string }{
		{"```yaml\ndescription: x\n```", "description: x"},
		{"```\ndescription: y\n```", "description: y"},
		{"description: z", "description: z"},
		{"```yaml\ndescription: a\n", "description: a"}, // 缺尾围栏也容忍
	}
	for _, c := range cases {
		if got := stripYAMLFence(c.in); got != c.want {
			t.Errorf("stripYAMLFence(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

// TestYAMLEditorMerge：合并保留原注释 + AI 初稿标注 + 追加缺失键。
func TestYAMLEditorMerge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wiki.yaml")
	orig := "# 人工注释\nproject:\n  description: 项目\n\ntables:\n  - name: order_tab\n    alias: 订单表\n"
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := loadYAMLEditor(path)
	if err != nil {
		t.Fatal(err)
	}
	e.setModuleDesc("example.com/app/internal/agent", "LLM 代理层")
	e.setTableAlias("user_tab", "用户表")
	e.setColumnComments("order_tab", map[string]string{"order_no": "订单号"})
	if err := e.save(path); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	s := string(b)
	for _, want := range []string{"# 人工注释", "# AI 初稿", "description: LLM 代理层", "alias: 用户表", "comment: 订单号"} {
		if !strings.Contains(s, want) {
			t.Errorf("合并结果缺 %q:\n%s", want, s)
		}
	}
	// 重新解析验证结构合法
	var cfg wikiConfig
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		t.Fatalf("合并后 yaml 解析失败: %v\n%s", err, s)
	}
	if len(cfg.Modules) != 1 || cfg.Modules[0].Description != "LLM 代理层" {
		t.Errorf("modules = %+v", cfg.Modules)
	}
	if len(cfg.Tables) != 2 {
		t.Errorf("tables = %+v", cfg.Tables)
	}
}

// aiBatchYAML 批量 AI 返回（对应 aiFixtureData 全部缺口：agent 模块、
// user_tab 表别名+列、order_tab 列 + 术语表）。
const aiBatchYAML = `modules:
  - name: example.com/app/internal/agent
    description: LLM 代理层：APIKey 管理
tables:
  - name: user_tab
    alias: 用户表
    columns:
      - name: id
        comment: 用户 ID
  - name: order_tab
    columns:
      - name: order_no
        comment: 订单号
glossary:
  - term: ORM
    definition: 对象关系映射——结构体与数据库表列的映射约定
`

// TestWikiAIFillEndToEnd：注入 runner——批量一次请求补全部缺口。
func TestWikiAIFillEndToEnd(t *testing.T) {
	data, cfg, cols := aiFixtureData()
	dir := t.TempDir()
	path := filepath.Join(dir, "wiki.yaml")
	if err := os.WriteFile(path, []byte("project:\n  description: 项目\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gotPrompt := ""
	restore := injectRunner(t, func(agent, prompt string, timeout time.Duration) (string, error) {
		gotPrompt = prompt
		return aiBatchYAML, nil
	})
	defer restore()
	rels := []*domain.TableRelation{
		{FromTable: "order_tab", FromCol: "order_id", ToTable: "user_tab", ToCol: "id", Type: domain.RelationFK},
	}
	// 缺口 = 模块 1 + 表 1（user_tab）+ 列 2（order_tab、user_tab）+ 术语 1
	ok, skip, fail := wikiAIFill(path, &cfg, data, cols, rels, "claude", 30*time.Second)
	if ok != 5 || skip != 0 || fail != 0 {
		t.Fatalf("计数 = %d/%d/%d; want 5/0/0", ok, skip, fail)
	}
	// 批量 prompt 一次带全部缺口（模块/表/列 + 关联事实 + 术语区块）
	for _, want := range []string{"example.com/app/internal/agent", "user_tab", "order_tab", "order_no", "order_tab.order_id → user_tab.id", "术语表"} {
		if !strings.Contains(gotPrompt, want) {
			t.Errorf("批量 prompt 缺 %q:\n%s", want, gotPrompt)
		}
	}
	// glossary 合并进 cfg
	found := false
	for _, g := range cfg.Glossary {
		if g.Term == "ORM" {
			found = true
		}
	}
	if !found {
		t.Errorf("cfg.Glossary 应含 ORM 术语: %+v", cfg.Glossary)
	}
	// cfg 同步更新（渲染用）——按名查找，不依赖追加顺序
	var agentDesc string
	for _, m := range cfg.Modules {
		if m.Name == "example.com/app/internal/agent" {
			agentDesc = m.Description
		}
	}
	if !strings.Contains(agentDesc, "LLM 代理层") {
		t.Errorf("cfg.Modules 应含 agent 描述: %+v", cfg.Modules)
	}
	foundAlias, foundComment, foundID := "", "", ""
	for _, tbl := range cfg.Tables {
		switch tbl.Name {
		case "user_tab":
			foundAlias = tbl.Alias
			for _, c := range tbl.Columns {
				if c.Name == "id" {
					foundID = c.Comment
				}
			}
		case "order_tab":
			for _, c := range tbl.Columns {
				if c.Name == "order_no" {
					foundComment = c.Comment
				}
			}
		}
	}
	if foundAlias != "用户表" {
		t.Errorf("cfg.Tables user_tab.Alias = %q; want 用户表", foundAlias)
	}
	if foundComment != "订单号" {
		t.Errorf("cfg.Tables order_tab order_no.Comment = %q; want 订单号", foundComment)
	}
	if foundID != "用户 ID" {
		t.Errorf("cfg.Tables user_tab id.Comment = %q; want 用户 ID", foundID)
	}
	// 文件落盘验证
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "# AI 初稿") {
		t.Errorf("文件应含 AI 初稿标注:\n%s", b)
	}
}


// aiSingleGapFixture 仅一个模块缺口（重试/失败测试用最小 fixture）。
func aiSingleGapFixture() ([]*domain.WikiModule, wikiConfig, []*domain.TableColumn) {
	data := []*domain.WikiModule{{Name: "example.com/app/internal/agent", ShortName: "agent", Desc: ""}}
	return data, wikiConfig{}, nil
}

// TestWikiAIFillRetryOnce：首次返回垃圾 → 重试一次成功。
func TestWikiAIFillRetryOnce(t *testing.T) {
	data, cfg, cols := aiSingleGapFixture()
	dir := t.TempDir()
	path := filepath.Join(dir, "wiki.yaml")
	os.WriteFile(path, []byte("project:\n  description: 项目\n"), 0o644)
	calls := 0
	restore := injectRunner(t, func(agent, prompt string, timeout time.Duration) (string, error) {
		calls++
		if calls <= 1 {
			return "完全不可解析的内容！！！", nil
		}
		return "modules:\n  - name: example.com/app/internal/agent\n    description: 重试成功", nil
	})
	defer restore()
	ok, _, fail := wikiAIFill(path, &cfg, data, cols, nil, "claude", 30*time.Second)
	if ok != 1 || fail != 0 || calls != 2 {
		t.Errorf("计数 = %d/%d 调用 %d; want 1 成功、重试一次", ok, fail, calls)
	}
}

// TestWikiAIFillFailTwice：两次都垃圾 → 丢弃 + 失败计数 + 文件无改动。
func TestWikiAIFillFailTwice(t *testing.T) {
	data, cfg, cols := aiSingleGapFixture()
	dir := t.TempDir()
	path := filepath.Join(dir, "wiki.yaml")
	orig := "project:\n  description: 项目\n"
	os.WriteFile(path, []byte(orig), 0o644)
	restore := injectRunner(t, func(agent, prompt string, timeout time.Duration) (string, error) {
		return "垃圾", nil
	})
	defer restore()
	ok, _, fail := wikiAIFill(path, &cfg, data, cols, nil, "claude", 30*time.Second)
	if ok != 0 || fail != 1 {
		t.Errorf("计数 = %d/%d; want 0/1", ok, fail)
	}
	b, _ := os.ReadFile(path)
	if string(b) != orig {
		t.Errorf("失败时文件不应改动:\n%s", b)
	}
}

// TestWikiAIFillEmptyYAML：空 wiki.yaml（wiki --ai 首跑场景）→ 补缺
// → save 落盘（回归：空 yaml.Node 编码失败会静默丢文件）。
func TestWikiAIFillEmptyYAML(t *testing.T) {
	data, cfg, cols := aiSingleGapFixture()
	dir := t.TempDir()
	path := filepath.Join(dir, "wiki.yaml")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	restore := injectRunner(t, func(agent, prompt string, timeout time.Duration) (string, error) {
		return "modules:\n  - name: example.com/app/internal/agent\n    description: 空文件首跑描述", nil
	})
	defer restore()
	ok, _, fail := wikiAIFill(path, &cfg, data, cols, nil, "claude", 30*time.Second)
	if ok != 1 || fail != 0 {
		t.Fatalf("计数 = %d/%d; want 1/0", ok, fail)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("wiki.yaml 应已落盘: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, "空文件首跑描述") || !strings.Contains(s, "# AI 初稿") {
		t.Errorf("落盘内容缺失:\n%s", s)
	}
}

