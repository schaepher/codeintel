package cli

// #0 wiki --ai 合并落盘测试：输出目录清理（用户文件保留）+ 空文件
// 首跑落盘（回归：--yaml 与 --out 同目录时 wiki.yaml 曾被删除）。

import (
	"fmt"
	"os"
	"path/filepath"
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
	write("wiki.yaml", "# 用户配置")           // 必须保留
	write("notes.md", "# 用户笔记")            // 非产物，保留
	write("index.md", "old")                  // 产物，删
	write("tables.md", "old")                 // 产物，删
	write("codeintel.md", "old")              // 模块页（本次渲染），删
	write("bench-sqlite.md", "old")           // 旧模块页（不在本次 data——来源不确定，保守保留）
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

// TestWikiAIFillSplitBatches：缺口 > aiBatchMax 分两批（第一批模块+
// 表，第二批列）——第二批 prompt 不再带已处理的模块。
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
	restore := injectRunner(t, func(agent, prompt string, timeout time.Duration) (string, error) {
		prompts = append(prompts, prompt)
		if strings.Contains(prompt, "表列中文说明") {
			// 第二批：列 + 术语
			return "tables:\n  - name: user_tab\n    columns:\n      - name: id\n        comment: 用户 ID\nglossary:\n  - term: ORM\n    definition: 对象关系映射", nil
		}
		// 第一批：61 个模块描述
		var b strings.Builder
		b.WriteString("modules:\n")
		for i := 0; i < 61; i++ {
			fmt.Fprintf(&b, "  - name: example.com/m%02d\n    description: 模块 %d\n", i, i)
		}
		return b.String(), nil
	})
	defer restore()
	ok, _, fail := wikiAIFill(path, &cfg, data, cols, nil, "claude", 30*time.Second, false, nil)
	// 61 模块 + 1 表 + 1 列组
	if ok != 63 || fail != 0 {
		t.Fatalf("计数 = %d/%d; want 63/0", ok, fail)
	}
	if len(prompts) != 2 {
		t.Fatalf("调用次数 = %d; want 2（分批）", len(prompts))
	}
	if !strings.Contains(prompts[0], "example.com/m00") {
		t.Errorf("第一批应含模块缺口:\n%s", prompts[0][:200])
	}
	if strings.Contains(prompts[1], "example.com/m00") || !strings.Contains(prompts[1], "表列中文说明") {
		t.Errorf("第二批不应带已处理模块、应只含列区:\n%s", prompts[1][:200])
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
	restore := injectRunner(t, func(agent, prompt string, timeout time.Duration) (string, error) {
		gotPrompt = prompt
		return aiBatchYAML, nil
	})
	defer restore()
	ok, _, fail := wikiAIFill(path, &cfg, data, cols, nil, "claude", 30*time.Second, true, repo)
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
	batches := splitGapBatches(nil, nil, colGaps, 60, 300)
	// 400 列 / 300 = 2 批（300 + 100）
	if len(batches) != 2 {
		t.Fatalf("批数 = %d; want 2（按列名数切）", len(batches))
	}
	if len(batches[0].colGaps) != 3 || len(batches[1].colGaps) != 1 {
		t.Errorf("分批 = %d/%d; want 3/1（每批 ≤300 列名）", len(batches[0].colGaps), len(batches[1].colGaps))
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
	restore := injectRunner(t, func(agent, prompt string, timeout time.Duration) (string, error) {
		called = true
		return "", nil
	})
	defer restore()
	ok, skip, fail := wikiAIFill(path, &cfg, data, cols, nil, "claude", 30*time.Second, false, nil)
	if ok != 0 || skip != 0 || fail != 0 {
		t.Errorf("无缺口计数 = %d/%d/%d; want 0/0/0", ok, skip, fail)
	}
	if called {
		t.Error("无缺口不应调用 AI")
	}
}
