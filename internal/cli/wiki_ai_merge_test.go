package cli

// #0 wiki --ai 合并落盘测试：输出目录清理（用户文件保留）+ 空文件
// 首跑落盘（回归：--yaml 与 --out 同目录时 wiki.yaml 曾被删除）。

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// TestCleanWikiOutDir：清理只删渲染产物——用户文件（wiki.yaml/笔记）
// 保留（回归：--yaml 与 --out 同目录时 wiki.yaml 曾被 RemoveAll 删掉）。
func TestCleanWikiOutDir(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("wiki.yaml", "# 用户配置")    // 必须保留
	write("notes.md", "# 用户笔记")     // 非产物，保留
	write("index.md", "old")        // 产物，删
	write("tables.md", "old")       // 产物，删
	write("codeintel.md", "old")    // 模块页（本次渲染），删
	write("bench-sqlite.md", "old") // 旧模块页（不在本次 data——来源不确定，保守保留）
	data := []*domain.WikiModule{{Name: "example.com/m", ShortName: "codeintel"}}
	if err := cleanWikiOutDir(dir, data); err != nil {
		t.Fatal(err)
	}
	remain := map[string]bool{}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		remain[e.Name()] = true
	}
	for _, want := range []string{"wiki.yaml", "notes.md", "bench-sqlite.md"} {
		if !remain[want] {
			t.Errorf("%s 应保留（保守：不确定来源不删）", want)
		}
	}
	for _, gone := range []string{"index.md", "tables.md", "codeintel.md"} {
		if remain[gone] {
			t.Errorf("%s 应被清理", gone)
		}
	}
}

// TestTableColBrief：列清单截断前 10 + 省略号（表别名 prompt 轻量化）。
func TestTableColBrief(t *testing.T) {
	if got := tableColBrief(nil); got != "" {
		t.Errorf("空 = %q; want 空", got)
	}
	cols := []string{"a", "b", "c"}
	if got := tableColBrief(cols); got != "a, b, c" {
		t.Errorf("≤10 = %q; want a, b, c", got)
	}
	cols = make([]string, 15)
	for i := range cols {
		cols[i] = "c" + string(rune('0'+i%10))
	}
	got := tableColBrief(cols)
	if len(got) <= 10 || !strings.HasSuffix(got, "…") {
		t.Errorf(">10 应截断 + 省略号: %q", got)
	}
	if !strings.HasSuffix(got, "c9…") || strings.Count(got, "c") != 10 {
		// 恰好前 10 列 + 省略号
		t.Errorf("截断内容 = %q; want 前 10 列 + …", got)
	}
}

// TestWikiAIFillSplitBatches：缺口 > aiBatchMax（20）切片多批——
// 每批 ≤20 条混合（模块/表/列组），同会话 resume。
func TestWikiAIFillSplitBatches(t *testing.T) {
	var data []*domain.WikiModule
	for i := 0; i < 61; i++ {
		data = append(data, &domain.WikiModule{Name: fmt.Sprintf("example.com/m%02d", i), ShortName: fmt.Sprintf("m%02d", i)})
	}
	cfg := wikiConfig{}
	cfg.Tables = []wikiTableConfig{{Name: "user_tab"}}
	cols := []*domain.TableColumn{{Name: "user_tab.id", ColType: "INTEGER"}}
	dir := t.TempDir()
	path := filepath.Join(dir, "wiki.yaml")
	os.WriteFile(path, []byte(""), 0o644)
	var prompts []string
	re := regexp.MustCompile(`example\.com/m\d\d`)
	modsOf := func(prompt string) string {
		var b strings.Builder
		b.WriteString("modules:\n")
		for _, name := range re.FindAllString(prompt, -1) {
			fmt.Fprintf(&b, "  - name: %s\n    description: %s 描述\n", name, name)
		}
		return b.String()
	}
	restore := injectRunner(t, func(agent, prompt string, timeout time.Duration, dir string) (string, error) {
		prompts = append(prompts, prompt)
		// 合并一批：一次调用返回完整 YAML（模块 + 表别名 + 列 + 术语）
		return modsOf(prompt) + "tables:\n  - name: user_tab\n    alias: 用户表\n    columns:\n      - name: id\n        comment: 用户 ID\nglossary:\n  - term: ORM\n    definition: 对象关系映射", nil
	})
	defer restore()
	ok, _, fail := wikiAIFill(path, &cfg, data, cols, nil, "claude", 30*time.Second, false, nil, "")
	// 61 模块 + 1 表别名 + 1 列说明 + 1 术语
	if ok != 64 || fail != 0 {
		t.Fatalf("计数 = %d/%d; want 64/0", ok, fail)
	}
	// 全部缺口合并一批——一次 AI 调用（用户明确预期）
	if len(prompts) != 1 {
		t.Fatalf("调用次数 = %d; want 1（全部缺口合并一批）", len(prompts))
	}
	if !strings.Contains(prompts[0], "example.com/m00") || !strings.Contains(prompts[0], "表列中文说明") {
		t.Errorf("单批 prompt 应含模块 + 列区:\n%s", prompts[0][:300])
	}
}

// TestWikiAIFillWithQA：--with-qa——相关历史 Q&A 进批量 prompt
// （按缺口表名/模块短名匹配 qa_history）。
func TestWikiAIFillWithQA(t *testing.T) {
	dir := seedRepo(t) // go.mod + db fixture
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := sqlite.NewRepo(db)
	// 预写历史问答：orders 表相关（匹配 order_tab？不——用 user_tab 相关）
	_ = repo.SaveQA(&domain.QARecord{Question: "user_tab 用途？", Answer: "用户表", Context: "user_tab", Agent: "claude", CreatedAt: 1})
	_ = repo.SaveQA(&domain.QARecord{Question: "无关", Answer: "无关", Context: "", Agent: "claude", CreatedAt: 2})

	data, cfg, cols := aiFixtureData()
	dir2 := t.TempDir()
	path := filepath.Join(dir2, "wiki.yaml")
	os.WriteFile(path, []byte(""), 0o644)
	gotPrompt := ""
	restore := injectRunner(t, func(agent, prompt string, timeout time.Duration, dir string) (string, error) {
		gotPrompt = prompt
		return aiBatchYAML, nil
	})
	defer restore()
	ok, _, fail := wikiAIFill(path, &cfg, data, cols, nil, "claude", 30*time.Second, true, repo, "")
	if ok != 5 || fail != 0 {
		t.Fatalf("计数 = %d/%d; want 5/0", ok, fail)
	}
	// 相关 Q&A（user_tab 匹配）进 prompt；无关的不进
	if !strings.Contains(gotPrompt, "user_tab 用途？") {
		t.Errorf("--with-qa prompt 应含相关历史问答:\n%s", gotPrompt)
	}
	if strings.Contains(gotPrompt, "无关") {
		t.Errorf("--with-qa prompt 不应含无关问答:\n%s", gotPrompt)
	}
}

// TestSplitGapBatchesCols：列组按列名数切片（每批 ≤ maxCols）——
// 大列数表不因组数少而撑爆 prompt。
func TestSplitGapBatchesCols(t *testing.T) {
	var colGaps []aiColGap
	for i := 0; i < 4; i++ {
		cols := make([]string, 100) // 每表 100 列
		colGaps = append(colGaps, aiColGap{table: fmt.Sprintf("t%d", i), cols: cols})
	}
	batches := splitGapBatches(nil, nil, colGaps, 300)
	// 全部合并一批——一次 AI 调用（用户明确预期；maxCols 参数保留兼容）
	if len(batches) != 1 {
		t.Fatalf("批数 = %d; want 1（全部缺口合并一次调用）", len(batches))
	}
	if len(batches[0].colGaps) != 4 {
		t.Errorf("分批 = %d; want 4（全部列组合并）", len(batches[0].colGaps))
	}
}

// TestWikiAIFillSkipNoGaps：无缺口 → 全跳过。
func TestWikiAIFillSkipNoGaps(t *testing.T) {
	cfg := wikiConfig{}
	cfg.Tables = []wikiTableConfig{{Name: "order_tab", Alias: "订单表",
		Columns: []wikiTableColumn{{Name: "id", Comment: "主键"}}}}
	data := []*domain.WikiModule{{Name: "example.com/app", ShortName: "app", Desc: "业务入口"}}
	cols := []*domain.TableColumn{{Name: "order_tab.id", ColType: "INTEGER"}}
	dir := t.TempDir()
	path := filepath.Join(dir, "wiki.yaml")
	os.WriteFile(path, []byte(""), 0o644)
	called := false
	restore := injectRunner(t, func(agent, prompt string, timeout time.Duration, dir string) (string, error) {
		called = true
		return "", nil
	})
	defer restore()
	ok, skip, fail := wikiAIFill(path, &cfg, data, cols, nil, "claude", 30*time.Second, false, nil, "")
	if ok != 0 || skip != 0 || fail != 0 {
		t.Errorf("无缺口计数 = %d/%d/%d; want 0/0/0", ok, skip, fail)
	}
	if called {
		t.Error("无缺口不应调用 AI")
	}
}

// TestSplitGapBatchesByCategory：方案 A——模块/表别名各一批（合并
// 全部缺口，不按条数切）；列说明单独切批。go2o 场景：148 表别名
// 只调 1 次 AI（原按 20 条/批 → 8 次）。
func TestSplitGapBatchesByCategory(t *testing.T) {
	var mods []aiModuleGap
	for i := 0; i < 30; i++ {
		mods = append(mods, aiModuleGap{name: fmt.Sprintf("m%d", i)})
	}
	var tbls []aiTableGap
	for i := 0; i < 148; i++ {
		tbls = append(tbls, aiTableGap{name: fmt.Sprintf("t%d", i)})
	}
	batches := splitGapBatches(mods, tbls, nil, 200)
	// 全部合并一批——一次 AI 调用（原按条数切：30/20+148/20=9 次）
	if len(batches) != 1 {
		t.Fatalf("批数 = %d; want 1（全部缺口合并一次调用）", len(batches))
	}
	if len(batches[0].mods) != 30 || len(batches[0].tbls) != 148 {
		t.Errorf("分批 = mods %d / tbls %d; want 全部合并 30/148",
			len(batches[0].mods), len(batches[0].tbls))
	}
}
