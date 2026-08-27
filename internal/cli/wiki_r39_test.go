package cli

// R39 自举循环（自身 wiki 实测暴露的三个渲染质量问题）：
// 1. entrySymbols 过滤噪音 main（tmp/ 前缀 + 文件不存在的幽灵 fixture）
// 2. grpcServiceList 过滤 0 方法服务（无内容子页）
// 3. 实体协作领域内图空串 → "（无内部协作）"（不渲染空 mermaid 块）
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

// TestEntrySymbolsFilters：main 入口噪音过滤——tmp/ 前缀（临时探针）
// 与文件不存在的幽灵 fixture（外部 module 残留）不进入入口列表。
func TestEntrySymbolsFilters(t *testing.T) {
	dir := seedRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	// 真实入口（main.go 已存在）+ tmp/ 探针 + 幽灵（文件不存在）
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m/tmp:main", Kind: domain.KindFunction,
			Name: "main", FilePath: "tmp/probe/main.go", LineStart: 3},
		{ID: "symbol:go:example.com/ghost:main", Kind: domain.KindFunction,
			Name: "main", FilePath: "ghost/main.go", LineStart: 3},
	}
	if _, err := r.SaveBatchStats(nodes, nil, nil); err != nil {
		t.Fatalf("save nodes: %v", err)
	}
	acts := action.New(sqlite.NewRepo(db))
	entries := entrySymbols(acts, dir)
	// 只有 seedRepo 的真实 main（example.com/m:main, main.go 存在）
	if len(entries) != 1 {
		t.Fatalf("入口数 = %d; want 1（tmp/ 探针 + 幽灵 main 应过滤）:\n%+v", len(entries), entries)
	}
	if entries[0].File != "main.go" {
		t.Errorf("保留入口 = %q; want main.go（真实文件）", entries[0].File)
	}
	// repoAbs 空（纯函数测试）→ 文件存在校验跳过，tmp/ 仍过滤
	entries2 := entrySymbols(acts, "")
	if len(entries2) != 2 {
		t.Errorf("repoAbs 空时入口数 = %d; want 2（只过滤 tmp/）", len(entries2))
	}
}

// TestGrpcServiceListSkipsEmpty：0 方法服务（无实现无 ServiceDesc——
// 自身 wiki 实测 Greeter）不进索引、不写子页。
func TestGrpcServiceListSkipsEmpty(t *testing.T) {
	dir := seedRoutesProcRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// seedRoutesProcRepo 的 QueryService 有 1 方法；补一个 0 方法服务
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m/grpc:svc.EmptyService", Kind: domain.KindGrpcService,
			Name: "svc.EmptyService", FilePath: "grpc/empty.pb.go",
			Properties: map[string]any{"service_name": "EmptyService"}},
	}, nil, nil); err != nil {
		t.Fatalf("save nodes: %v", err)
	}
	acts := action.New(sqlite.NewRepo(db))
	rc := &wikiRenderCtx{acts: acts, cfg: wikiConfig{
		Domains: []wikiDomainCfg{{Name: "其他", Packages: []string{"impl"}}},
	}}
	svcs := grpcServiceList(rc)
	for _, s := range svcs {
		if s.Name == "EmptyService" {
			t.Error("0 方法服务不应出现在服务列表（无内容子页）")
		}
	}
	found := false
	for _, s := range svcs {
		if s.Name == "QueryService" {
			found = true
		}
	}
	if !found {
		t.Error("有方法服务应保留")
	}
}

// TestEntityDomainEmptyInner：领域仅 1 实体无内部协作——渲染文字说明
// 而非空 mermaid 块（自身 wiki 实测"基础支撑域 1 实体"空图）。
func TestEntityDomainEmptyInner(t *testing.T) {
	g := &domain.EntityGraph{
		Nodes: []*domain.EntityNode{
			{ID: "symbol:go:example.com/logging:logging", Name: "logging", Pkg: "example.com/logging", Kind: domain.EntityKindPkgFace, FreeFuncs: 5},
			// 第二域实体（有 1 条强边——保证领域内图分支走非空）
			{ID: "symbol:go:example.com/nope:ghost", Name: "ghost", Pkg: "example.com/nope", Kind: domain.EntityKindStruct},
		},
		Edges: []*domain.EntityEdge{
			{From: "symbol:go:example.com/nope:ghost", To: "symbol:go:example.com/nope:ghost", Count: 5},
		},
		ByName: map[string][]string{},
	}
	rc := &wikiRenderCtx{cfg: wikiConfig{
		Domains: []wikiDomainCfg{
			{Name: "基础支撑域", Packages: []string{"logging"}},
			{Name: "其他", Packages: []string{"nope"}},
		},
	}, Diagram: "mermaid"}
	m := renderEntitiesSectionMD(g, rc)
	if strings.Contains(m, "```mermaid\n\n```") {
		t.Errorf("空 mermaid 块不应渲染:\n%s", m)
	}
	if !strings.Contains(m, "（无内部协作）") {
		t.Error("应渲染（无内部协作）说明")
	}
}

// TestEntrySymbolsFilterEndToEnd：cmdWiki 端到端——tmp/ main 不出现
// 在 processes.md 入口节。
func TestEntrySymbolsFilterEndToEnd(t *testing.T) {
	dir := seedRoutesProcRepo(t)
	// 加 tmp/ 探针 main（真实文件存在——验证 tmp/ 前缀过滤而非文件校验）
	if err := os.MkdirAll(filepath.Join(dir, "tmp", "probe"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tmp", "probe", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := sqlite.NewRepo(db).SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m/tmp:main", Kind: domain.KindFunction,
			Name: "main", FilePath: "tmp/probe/main.go", LineStart: 3},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "wiki")
	if code := cmdWiki([]string{"--repo", dir, "--out", out}); code != 0 {
		t.Fatalf("cmdWiki exit = %d", code)
	}
	proc, err := os.ReadFile(filepath.Join(out, "processes.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(proc), "tmp/probe") {
		t.Error("processes.md 不应含 tmp/ 探针入口")
	}
}
