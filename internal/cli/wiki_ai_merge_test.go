package cli

// #0 wiki --ai 合并落盘测试：输出目录清理（用户文件保留）+ 空文件
// 首跑落盘（回归：--yaml 与 --out 同目录时 wiki.yaml 曾被删除）。

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
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
	ok, skip, fail := wikiAIFill(path, &cfg, data, cols, nil, "claude", 30*time.Second)
	if ok != 0 || skip != 0 || fail != 0 {
		t.Errorf("无缺口计数 = %d/%d/%d; want 0/0/0", ok, skip, fail)
	}
	if called {
		t.Error("无缺口不应调用 AI")
	}
}
