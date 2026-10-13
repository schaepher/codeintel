package cli

// F1 遗留修复：processes 页不再硬编码 codeintel 自身命令（go2o 验收
// 暴露：12 个 codeintel 命令在目标索引全无调用链——wiki-check 时序
// FAIL），改为目标仓库 main 入口生成（对齐 commands 页 F1 方案）。
// 测试先行。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// TestRenderProcessesFromEntry：processes 页从目标仓库 main 入口生成
// ——不再含 codeintel 自身命令（init/update/serve/...），改为主入口 +
// 一级调用展开。
func TestRenderProcessesFromEntry(t *testing.T) {
	dir := seedRepo(t) // fixture: main → (Svc).Run（example.com/m）
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	acts := action.New(sqlite.NewRepo(db))

	m := renderProcessesMD(&wikiRenderCtx{acts: acts, Diagram: "mermaid"})
	// 不再硬编码 codeintel 自身命令
	for _, bad := range []string{"init —— 全量构建索引", "cmdInit", "cmdWiki", "cmdServe", "codeintel"} {
		if strings.Contains(m, bad) {
			t.Errorf("processes 页不应含 codeintel 自身命令 %q:\n%s", bad, m)
		}
	}
	// 改为目标仓库 main 入口 + 一级调用
	for _, want := range []string{"# 系统流程", "## 入口 `main`", "main.go", "(Svc).Run"} {
		if !strings.Contains(m, want) {
			t.Errorf("processes 页应含 %q:\n%s", want, m)
		}
	}

	h := renderProcessesHTML(&wikiRenderCtx{acts: acts, Diagram: "mermaid"})
	for _, want := range []string{`<h3>入口 <code>main</code>`, "<code>svc:(Svc).Run</code>"} {
		if !strings.Contains(h, want) {
			t.Errorf("processes html 应含 %q:\n%s", want, h)
		}
	}
	for _, bad := range []string{"init —— 全量构建索引", "cmdInit"} {
		if strings.Contains(h, bad) {
			t.Errorf("processes html 不应含 codeintel 自身命令 %q:\n%s", bad, h)
		}
	}
}

// TestRenderProcessesSharedBudget：多个 main 入口共享 --max-entries 总
// 展开预算——预算内的一级调用展开，其余保留为清单（不再每入口各自全量
// 展开）。Markdown 与 HTML 共用同一预算。
func TestRenderProcessesSharedBudget(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	m1 := domain.CanonicalID("symbol:go:example.com/m:main")
	m2 := domain.CanonicalID("symbol:go:example.com/m/cmd:main")
	var nodes []*domain.CodeEntity
	nodes = append(nodes,
		&domain.CodeEntity{ID: m1, Kind: domain.KindFunction, Name: "main", FilePath: "main.go"},
		&domain.CodeEntity{ID: m2, Kind: domain.KindFunction, Name: "main", FilePath: "cmd/main.go"},
	)
	var edges []*domain.Fact
	for i, c := range []string{"c1", "c2", "c3", "c4"} {
		cid := domain.CanonicalID("symbol:go:example.com/m:" + c)
		nodes = append(nodes, &domain.CodeEntity{ID: cid, Kind: domain.KindFunction, Name: c, FilePath: c + ".go"})
		src := m1
		if i >= 2 {
			src = m2
		}
		edges = append(edges, &domain.Fact{SourceID: src, TargetID: cid, Kind: domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.8})
	}
	if _, err := r.SaveBatchStats(nodes, edges, nil); err != nil {
		t.Fatal(err)
	}
	acts := action.New(r)
	rc := &wikiRenderCtx{acts: acts, Diagram: "mermaid", MaxEntries: 2}

	md := renderProcessesMD(rc)
	if got := strings.Count(md, "### "); got != 2 {
		t.Errorf("Markdown 展开数 = %d; want 2（共享预算）:\n%s", got, md)
	}
	if !strings.Contains(md, "其余一级调用（未展开，共 2）") {
		t.Errorf("Markdown 应列其余一级调用清单:\n%s", md)
	}
	html := renderProcessesHTML(rc)
	if got := strings.Count(html, "<h4>"); got != 2 {
		t.Errorf("HTML 展开数 = %d; want 2（共享预算）:\n%s", got, html)
	}
	if !strings.Contains(html, "其余一级调用（未展开，共 2）") {
		t.Errorf("HTML 应列其余一级调用清单:\n%s", html)
	}
}
