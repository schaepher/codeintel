package cli

// R38 服务 → 领域映射测试（从 wiki_processes_routes_test.go 拆出——
// 行数治理）：yaml domains.services 显式优先 / 调用链投票兜底 /
// 无匹配「其他」；索引按领域分组。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
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
	svc := action.GrpcRouteService{Name: "QueryService", Impl: "queryServiceImpl",
		ImplID:  "symbol:go:example.com/m/impl:queryServiceImpl",
		Methods: []action.GrpcRouteMethod{{Name: "Query", Handler: "_QueryService_Query_Handler"}}}

	// 1. yaml services 显式优先
	rcExp := &wikiRenderCtx{acts: acts, repo: sqlite.NewRepo(db), cfg: wikiConfig{
		Domains: []wikiDomainCfg{{Name: "查询域", Services: []string{"QueryService"}}},
	}}
	if d := serviceDomain(rcExp, svc); d != "查询域" {
		t.Errorf("yaml 显式 = %q; want 查询域", d)
	}
	// 2. 无显式 → 调用链投票。R78：投票排除服务实现包（impl）——
	// 追加业务包调用（queryHelper → business）后，业务包命中；
	// 仅实现包时排除后无票 → 空（不被实现包所在域单方面赢得投票）
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m/business:(orderSvc).Do", Kind: domain.KindMethod,
			Name: "(orderSvc).Do", FilePath: "business/order.go", LineStart: 10},
	}, []*domain.Fact{
		{SourceID: "symbol:go:example.com/m/impl:queryHelper", TargetID: "symbol:go:example.com/m/business:(orderSvc).Do",
			Kind: domain.FactCalls, Confidence: 0.8},
	}, nil); err != nil {
		t.Fatal(err)
	}
	rcVote := &wikiRenderCtx{acts: acts, repo: sqlite.NewRepo(db), cfg: wikiConfig{
		Domains: []wikiDomainCfg{
			{Name: "实现域", Packages: []string{"impl"}},
			{Name: "业务域", Packages: []string{"business"}},
		},
	}}
	if d := serviceDomain(rcVote, svc); d != "业务域" {
		t.Errorf("调用链投票（排除实现包） = %q; want 业务域", d)
	}
	rcImplOnly := &wikiRenderCtx{acts: acts, repo: sqlite.NewRepo(db), cfg: wikiConfig{
		Domains: []wikiDomainCfg{{Name: "实现域", Packages: []string{"impl"}}},
	}}
	if d := serviceDomain(rcImplOnly, svc); d != "" {
		t.Errorf("仅实现包调用 = %q; want 空（实现包不投票）", d)
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
	svcs := []action.GrpcRouteService{
		{Name: "QueryService", Impl: "queryServiceImpl",
			ImplID:  "symbol:go:example.com/m/impl:queryServiceImpl",
			Methods: []action.GrpcRouteMethod{{Name: "Query"}}},
		{Name: "AnotherService", Impl: "anotherImpl",
			ImplID:  "symbol:go:example.com/m/impl:anotherImpl",
			Methods: []action.GrpcRouteMethod{{Name: "Do"}}},
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
