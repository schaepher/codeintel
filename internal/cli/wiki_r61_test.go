package cli

// R62 五连改测试（用户需求）：设计诊断折叠（默认折叠）、领域内实体
// 协作全展示（弱边/孤立实体不丢）、grpc 服务方法表格、包结构方法/
// 函数表格。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// TestEntitiesSectionDiagFolded：设计诊断默认折叠（details 无 open）。
func TestEntitiesSectionDiagFolded(t *testing.T) {
	g := &domain.EntityGraph{Nodes: []*domain.EntityNode{
		{ID: "A", Name: "A", Pkg: "example.com/m/a"},
		{ID: "B", Name: "B", Pkg: "example.com/m/b"},
	}, Edges: []*domain.EntityEdge{{From: "A", To: "B", Count: 5}},
		Diags: []*domain.EntityDiag{{Kind: domain.DiagCycle, Target: "A/B", Detail: "循环"}}}
	rc := &wikiRenderCtx{Diagram: "mermaid"}
	md := renderEntitiesSectionMD(g, rc)
	if !strings.Contains(md, "<details><summary>") {
		t.Errorf("md 设计诊断应为折叠形态:\n%s", md)
	}
	if strings.Contains(md, "设计诊断") && !strings.Contains(md, "展开查看") {
		// 设计诊断内容应在 details 内
		if i := strings.Index(md, "设计诊断"); i >= 0 {
			if j := strings.Index(md, "<details>"); j < 0 || j > i {
				t.Errorf("设计诊断应包在 details 内（默认折叠）:\n%s", md)
			}
		}
	}
	html := renderEntitiesSectionHTML(g, rc)
	if !strings.Contains(html, "<details><summary>设计诊断") {
		t.Errorf("html 设计诊断应为折叠形态:\n%s", html)
	}
	if strings.Contains(html, "<details open") {
		t.Error("设计诊断默认折叠——不应 open")
	}
}

// TestEntityMermaidAllNodes：弱边/孤立实体不丢（R62——领域内实体协作
// 不全：entityMermaid 只画强边实体）。
func TestEntityMermaidAllNodes(t *testing.T) {
	g := &domain.EntityGraph{Nodes: []*domain.EntityNode{
		{ID: "A", Name: "A", Pkg: "example.com/m/a"},
		{ID: "Lonely", Name: "Lonely", Pkg: "example.com/m/l"},
	}, Edges: []*domain.EntityEdge{{From: "A", To: "Lonely", Count: 1}}} // 弱边（<3）
	got := entityMermaid(g)
	if !strings.Contains(got, "Lonely") {
		t.Errorf("弱边实体应出现在图中（不全展示 bug）:\n%s", got)
	}
	if strings.Contains(got, "-->|1|") {
		t.Errorf("弱边不应画边（防噪音）:\n%s", got)
	}
	if !strings.Contains(got, "A") {
		t.Errorf("强边实体 A 应出现:\n%s", got)
	}
}

// TestGrpcServicePageMethodsTable：服务页含全部方法表格（方法/handler/
// 调用链状态——图太严格时至少表格兜底）。
func TestGrpcServicePageMethodsTable(t *testing.T) {
	dir := seedRoutesProcRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	acts := action.New(sqlite.NewRepo(db))
	svc := grpcRouteService{Name: "QueryService", Impl: "queryServiceImpl",
		ImplID: "symbol:go:example.com/m/impl:queryServiceImpl",
		Methods: []grpcRouteMethod{
			{Name: "Query", Handler: "_QueryService_Query_Handler"},
			{Name: "PagingShops", Handler: "_QueryService_PagingShops_Handler"},
		}}
	md := renderGrpcServiceMD(&wikiRenderCtx{acts: acts, repo: sqlite.NewRepo(db), Diagram: "mermaid"}, svc, 15)
	for _, want := range []string{"| 方法 |", "Query", "_QueryService_Query_Handler", "PagingShops"} {
		if !strings.Contains(md, want) {
			t.Errorf("服务页应含方法表格 %q:\n%s", want, md)
		}
	}
	html := renderGrpcServiceHTML(&wikiRenderCtx{acts: acts, repo: sqlite.NewRepo(db), Diagram: "mermaid"}, svc, 15)
	if !strings.Contains(html, "<table") {
		t.Errorf("html 服务内容应含方法表格:\n%s", html)
	}
}

// TestPackagesMethodsTable：包结构无包说明 fallback——方法/函数列表格。
func TestPackagesMethodsTable(t *testing.T) {
	pkgs := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m/pkg:pkg", Kind: domain.KindPackage, Name: "example.com/m/pkg"},
	}
	// 无 doc_comment → fallback 代码事实（需 repo 有 struct/method 节点）
	dir := seedRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m/pkg:(T).Run", Kind: domain.KindMethod, Name: "(T).Run",
			Properties: map[string]any{"signature": "func (t *T) Run(ctx context.Context) error"}},
		{ID: "symbol:go:example.com/m/pkg:F1", Kind: domain.KindFunction, Name: "F1",
			Properties: map[string]any{"signature": "func F1(x int) string"}},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	md := renderPackagesMD(pkgs, r)
	for _, want := range []string{"| 方法 |", "(T).Run", "| 函数 |", "F1"} {
		if !strings.Contains(md, want) {
			t.Errorf("包结构 md 应含方法/函数表格 %q:\n%s", want, md)
		}
	}
	html := renderPackagesHTML(pkgs, r)
	if !strings.Contains(html, "<table") {
		t.Errorf("包结构 html 应含表格:\n%s", html)
	}
}
