package cli

// R38 服务 → 领域映射测试（从 wiki_processes_routes_test.go 拆出——
// 行数治理）：yaml domains.services 显式优先 / 调用链投票兜底 /
// 无匹配「其他」；索引按领域分组。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// TestServiceDomain：服务归属领域——yaml domains.services 显式优先；
// 无显式走调用链涉及包投票（fixture QueryService 链涉及 impl 包——
// 配置域含 impl 包则命中）；无匹配返回空（渲染为「其他」）。
func TestServiceDomain(t *testing.T) {
	dir := seedRoutesProcRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	acts := action.New(sqlite.NewRepo(db))
	svc := grpcRouteService{Name: "QueryService", Impl: "queryServiceImpl",
		ImplID: "symbol:go:example.com/m/impl:queryServiceImpl",
		Methods: []grpcRouteMethod{{Name: "Query", Handler: "_QueryService_Query_Handler"}}}

	// 1. yaml services 显式优先
	rcExp := &wikiRenderCtx{acts: acts, repo: sqlite.NewRepo(db), cfg: wikiConfig{
		Domains: []wikiDomainCfg{{Name: "查询域", Services: []string{"QueryService"}}},
	}}
	if d := serviceDomain(rcExp, svc); d != "查询域" {
		t.Errorf("yaml 显式 = %q; want 查询域", d)
	}
	// 2. 无显式 → 调用链投票（链涉及 example.com/m/impl——配置域包 impl）
	rcVote := &wikiRenderCtx{acts: acts, repo: sqlite.NewRepo(db), cfg: wikiConfig{
		Domains: []wikiDomainCfg{{Name: "实现域", Packages: []string{"impl"}}},
	}}
	if d := serviceDomain(rcVote, svc); d != "实现域" {
		t.Errorf("调用链投票 = %q; want 实现域", d)
	}
	// 3. 无匹配 → 空（其他）
	rcNone := &wikiRenderCtx{acts: acts, repo: sqlite.NewRepo(db), cfg: wikiConfig{
		Domains: []wikiDomainCfg{{Name: "无关域", Packages: []string{"nope"}}},
	}}
	if d := serviceDomain(rcNone, svc); d != "" {
		t.Errorf("无匹配 = %q; want 空", d)
	}
}

// TestGrpcIndexByDomain：索引按领域分组——领域标题 + 目录路径链接，
// 「其他」排最后。
func TestGrpcIndexByDomain(t *testing.T) {
	dir := seedRoutesProcRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	acts := action.New(sqlite.NewRepo(db))
	svcs := []grpcRouteService{
		{Name: "QueryService", Impl: "queryServiceImpl",
			ImplID: "symbol:go:example.com/m/impl:queryServiceImpl",
			Methods: []grpcRouteMethod{{Name: "Query"}}},
		{Name: "AnotherService", Impl: "anotherImpl",
			ImplID: "symbol:go:example.com/m/impl:anotherImpl",
			Methods: []grpcRouteMethod{{Name: "Do"}}},
	}
	rc := &wikiRenderCtx{acts: acts, repo: sqlite.NewRepo(db), cfg: wikiConfig{
		Domains: []wikiDomainCfg{
			{Name: "实现域", Packages: []string{"impl"}, Services: []string{"QueryService"}},
		},
	}}
	m := renderGrpcIndexMD(rc, svcs, 15)
	for _, want := range []string{"### 领域 实现域", "### 领域 其他", "实现域/processes-grpc-QueryService.md", "其他/processes-grpc-AnotherService.md"} {
		if !strings.Contains(m, want) {
			t.Errorf("索引应含 %q:\n%s", want, m)
		}
	}
	if strings.Index(m, "领域 实现域") > strings.Index(m, "领域 其他") {
		t.Error("「其他」应排最后")
	}
}
