package cli

// R37 流程页基于 http/grpc 入口（待办 4）：main 入口节保留 + HTTP 路由
// 入口节（handler_id 展开、同 handler 去重、resolver 分组）+ gRPC 服务
// 方法入口（每服务子页，页内上限折叠）。测试先行。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedRoutesProcRepo 预填路由入口图：http_route 节点（handler_id）+ handler
// 函数 + 一级调用边；grpc_service 节点 + grpc_impl 边 + 实现类型方法。
func seedRoutesProcRepo(t *testing.T) string {
	t.Helper()
	dir := seedRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		// HTTP 路由（native + gin；同 handler 去重：/ping 与 /v1/ping 同 handler）
		{ID: "symbol:go:example.com/m/api:route.1", Kind: domain.KindHTTPRoute,
			Name: "GET /ping", Properties: map[string]any{
				"method": "GET", "path": "/ping", "handler": "pingHandler",
				"handler_id": "symbol:go:example.com/m/api:pingHandler",
				"resolver":   "gin", "register": "api/routes.go:20",
			}},
		{ID: "symbol:go:example.com/m/api:route.2", Kind: domain.KindHTTPRoute,
			Name: "GET /v1/ping", Properties: map[string]any{
				"method": "GET", "path": "/v1/ping", "handler": "pingHandler",
				"handler_id": "symbol:go:example.com/m/api:pingHandler",
				"resolver":   "gin", "register": "api/routes.go:21",
			}},
		{ID: "symbol:go:example.com/m/api:route.3", Kind: domain.KindHTTPRoute,
			Name: " /", Properties: map[string]any{
				"method": "", "path": "/", "handler": "home",
				"resolver": "native", "register": "api/api.go:12",
			}},
		// HTTP handler 函数 + 一级调用
		{ID: "symbol:go:example.com/m/api:pingHandler", Kind: domain.KindFunction,
			Name: "pingHandler", FilePath: "api/routes.go", LineStart: 10},
		{ID: "symbol:go:example.com/m/api:doPing", Kind: domain.KindFunction,
			Name: "doPing", FilePath: "api/routes.go", LineStart: 15},
		{ID: "symbol:go:example.com/m/api:home", Kind: domain.KindFunction,
			Name: "home", FilePath: "api/api.go", LineStart: 8},
		// gRPC 服务 + 实现类型 + 方法
		{ID: "symbol:go:example.com/m/grpc:svc.QueryService", Kind: domain.KindGrpcService,
			Name: "svc.QueryService", FilePath: "grpc/query_grpc.pb.go",
			Properties: map[string]any{"service_name": "QueryService", "methods": "Query"}},
		{ID: "symbol:go:example.com/m/impl:queryServiceImpl", Kind: domain.KindStruct,
			Name: "queryServiceImpl", FilePath: "impl/query.go", LineStart: 20},
		{ID: "symbol:go:example.com/m/impl:(queryServiceImpl).Query", Kind: domain.KindMethod,
			Name: "(queryServiceImpl).Query", FilePath: "impl/query.go", LineStart: 25},
		{ID: "symbol:go:example.com/m/impl:queryHelper", Kind: domain.KindFunction,
			Name: "queryHelper", FilePath: "impl/query.go", LineStart: 30},
		// R55：ServiceDesc 方法名小写（proto 定义名 sendCode）→ 实现是
		// Go 导出名（SendCode）——handler 提取场景
		{ID: "symbol:go:example.com/m/impl:checkServiceImpl", Kind: domain.KindStruct,
			Name: "checkServiceImpl", FilePath: "impl/check.go", LineStart: 10},
		{ID: "symbol:go:example.com/m/impl:(checkServiceImpl).SendCode", Kind: domain.KindMethod,
			Name: "(checkServiceImpl).SendCode", FilePath: "impl/check.go", LineStart: 15},
		{ID: "symbol:go:example.com/m/impl:checkHelper", Kind: domain.KindFunction,
			Name: "checkHelper", FilePath: "impl/check.go", LineStart: 20},
	}
	if _, err := r.SaveBatchStats(nodes, nil, nil); err != nil {
		t.Fatalf("save nodes: %v", err)
	}
	facts := []*domain.Fact{
		// grpc_impl：实现类型 → 服务
		{SourceID: "symbol:go:example.com/m/impl:queryServiceImpl", TargetID: "symbol:go:example.com/m/grpc:svc.QueryService",
			Kind: domain.FactGrpcImpl, Confidence: 1.0},
		// handler 一级调用（line_num 决定步骤顺序）
		{SourceID: "symbol:go:example.com/m/api:pingHandler", TargetID: "symbol:go:example.com/m/api:doPing",
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": 11}},
		{SourceID: "symbol:go:example.com/m/api:home", TargetID: "symbol:go:example.com/m/api:doPing",
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": 9}},
		// grpc 方法一级调用
		{SourceID: "symbol:go:example.com/m/impl:(queryServiceImpl).Query", TargetID: "symbol:go:example.com/m/impl:queryHelper",
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": 26}},
		// R55：小写 proto 名方法的实现（Go 导出名）调用链
		{SourceID: "symbol:go:example.com/m/impl:(checkServiceImpl).SendCode", TargetID: "symbol:go:example.com/m/impl:checkHelper",
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": 16}},
	}
	if _, err := r.SaveBatchStats(nil, facts, nil); err != nil {
		t.Fatalf("save facts: %v", err)
	}
	return dir
}

// TestProcHTTPEntries：httpProcEntries——handler_id 展开、同 handler 去重
// （/ping + /v1/ping 合并为一个入口）、匿名/无 handler_id 降级。
func TestProcHTTPEntries(t *testing.T) {
	dir := seedRoutesProcRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	acts := action.New(sqlite.NewRepo(db))
	entries := httpProcEntries(acts)
	if len(entries) != 2 {
		t.Fatalf("入口数 = %d; want 2（pingHandler 去重 + home）:\n%+v", len(entries), entries)
	}
	for _, e := range entries {
		switch e.Handler {
		case "pingHandler":
			if len(e.Paths) != 2 || e.Paths[0] != "GET /ping" || e.Paths[1] != "GET /v1/ping" {
				t.Errorf("pingHandler 路径 = %v; want [GET /ping GET /v1/ping]（同 handler 去重）", e.Paths)
			}
			if e.Chain == nil || len(e.Chain.Steps) == 0 {
				t.Error("pingHandler 应有调用链（handler_id 解析成功）")
			}
		case "home":
			if e.Chain == nil {
				t.Error("home 应有调用链")
			}
		}
	}
}

// TestRenderProcessesRoutes：流程页含 main 节 + HTTP 路由节 + gRPC 服务
// 索引（main 节保留——R37 用户定案）。
func TestRenderProcessesRoutes(t *testing.T) {
	dir := seedRoutesProcRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	acts := action.New(sqlite.NewRepo(db))
	m := renderProcessesMD(&wikiRenderCtx{acts: acts, repo: sqlite.NewRepo(db), Diagram: "mermaid"})
	for _, want := range []string{
		"# 系统流程", "## 入口 `main`", // main 节保留
		"## HTTP 路由入口", "GET /ping", "GET /v1/ping", "doPing", // 路由节 + 展开
		"## gRPC 服务入口", "QueryService", "processes-grpc-QueryService.md", // 服务索引
	} {
		if !strings.Contains(m, want) {
			t.Errorf("processes md 应含 %q", want)
		}
	}
	h := renderProcessesHTML(&wikiRenderCtx{acts: acts, repo: sqlite.NewRepo(db), Diagram: "mermaid"})
	for _, want := range []string{
		`<h2>系统流程</h2>`, "HTTP 路由入口", "gRPC 服务入口", "服务 QueryService",
	} {
		if !strings.Contains(h, want) {
			t.Errorf("processes html 应含 %q", want)
		}
	}
}

// TestGrpcServicePageMD：gRPC 服务子页——方法入口逐个展开（图 + 涉及包）。
func TestGrpcServicePageMD(t *testing.T) {
	dir := seedRoutesProcRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	acts := action.New(sqlite.NewRepo(db))
	svc := action.GrpcRouteService{Name: "QueryService", Impl: "queryServiceImpl",
		ImplID: "symbol:go:example.com/m/impl:queryServiceImpl",
		Methods: []action.GrpcRouteMethod{
			{Name: "Query", Handler: "_QueryService_Query_Handler"},
		}}
	page := renderGrpcServiceMD(&wikiRenderCtx{acts: acts, repo: sqlite.NewRepo(db), Diagram: "mermaid"}, svc, 15)
	for _, want := range []string{"QueryService", "(queryServiceImpl).Query", "queryHelper", "涉及包"} {
		if !strings.Contains(page, want) {
			t.Errorf("服务页应含 %q", want)
		}
	}
}

// TestWikiGrpcSubpages：cmdWiki 端到端——R40：html 单文件——服务流程
// 内容内嵌进 index.html（<details> 折叠，不写独立子页文件）；md 模式
// 仍按领域分目录写子页。
func TestWikiGrpcSubpages(t *testing.T) {
	dir := seedRoutesProcRepo(t)
	out := filepath.Join(t.TempDir(), "wiki")
	if code := cmdWiki([]string{"--repo", dir, "--out", out, "--format", "html"}); code != 0 {
		t.Fatalf("cmdWiki html exit = %d", code)
	}
	idx, err := os.ReadFile(filepath.Join(out, "index.html"))
	if err != nil {
		t.Fatalf("index.html 未生成: %v", err)
	}
	// 服务内容内嵌（details 折叠 + 方法展开）
	for _, want := range []string{"gRPC 服务入口", "服务 QueryService", "(queryServiceImpl).Query"} {
		if !strings.Contains(string(idx), want) {
			t.Errorf("index.html 应内嵌服务内容 %q", want)
		}
	}
	// html 单文件：不写独立子页文件（R40）
	if _, err := os.Stat(filepath.Join(out, "其他", "processes-grpc-QueryService.html")); err == nil {
		t.Error("html 模式不应生成独立子页文件（R40 单文件）")
	}
	out2 := filepath.Join(t.TempDir(), "wiki2")
	if code := cmdWiki([]string{"--repo", dir, "--out", out2}); code != 0 {
		t.Fatalf("cmdWiki md exit = %d", code)
	}
	proc, err := os.ReadFile(filepath.Join(out2, "processes.md"))
	if err != nil {
		t.Fatalf("processes.md 未生成: %v", err)
	}
	if !strings.Contains(string(proc), "其他/processes-grpc-QueryService.md") {
		t.Error("processes.md 应链接 gRPC 服务子页（md 多文件保留）")
	}
	if _, err := os.Stat(filepath.Join(out2, "其他", "processes-grpc-QueryService.md")); err != nil {
		t.Error("md 输出应生成服务子页（其他目录）")
	}
}

// TestProcFoldMaxEntries：入口超上限折叠——超出部分只列清单（md 平铺）。
func TestProcFoldMaxEntries(t *testing.T) {
	dir := seedRoutesProcRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	acts := action.New(sqlite.NewRepo(db))
	entries := httpProcEntries(acts)
	m := renderHTTPRoutesMD(&wikiRenderCtx{acts: acts, repo: sqlite.NewRepo(db), Diagram: "mermaid"}, entries, 1)
	if !strings.Contains(m, "其余 1 个入口仅列清单") {
		t.Errorf("超上限应标注折叠提示:\n%s", m)
	}
	if strings.Count(m, "```mermaid") > 1 {
		t.Errorf("折叠后只应展开 1 个入口的图，实际:\n%s", m)
	}
}
