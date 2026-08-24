package cli

// R31 query http-routes 测试：http_route 节点（构建期两个 resolver
// 发射）→ 契约 JSON。测试先行。

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedHTTPRoutesRepo 构造含 http_route 节点的索引（native + gin 混合）。
func seedHTTPRoutesRepo(t *testing.T) string {
	t.Helper()
	dir := seedRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m/api:route.1", Kind: domain.KindHTTPRoute,
			Name: " /", Properties: map[string]any{
				"method": "", "path": "/", "handler": "home",
				"resolver": "native", "register": "api/api.go:12",
			}},
		{ID: "symbol:go:example.com/m/api:route.2", Kind: domain.KindHTTPRoute,
			Name: "GET /ping", Properties: map[string]any{
				"method": "GET", "path": "/ping", "handler": "pingHandler",
				"resolver": "gin", "register": "api/routes.go:20",
			}},
		{ID: "symbol:go:example.com/m/api:route.3", Kind: domain.KindHTTPRoute,
			Name: "POST /api/orders", Properties: map[string]any{
				"method": "POST", "path": "/api/orders", "handler": "createOrder",
				"resolver": "gin", "register": "api/routes.go:24",
			}},
	}
	if _, err := r.SaveBatchStats(nodes, nil, nil); err != nil {
		t.Fatalf("save nodes: %v", err)
	}
	return dir
}

// TestQueryHTTPRoutes：JSON 契约——method/path/handler/resolver/register。
func TestQueryHTTPRoutes(t *testing.T) {
	dir := seedHTTPRoutesRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	res, err := httpRoutes(sqlite.NewRepo(db))
	if err != nil {
		t.Fatalf("httpRoutes: %v", err)
	}
	if len(res.Routes) != 3 {
		t.Fatalf("路由数 = %d; want 3:\n%+v", len(res.Routes), res)
	}
	// 确定性排序：resolver（gin < native）→ method（"" < GET < POST）→ path
	if res.Routes[0].Resolver != "gin" || res.Routes[0].Method != "GET" || res.Routes[0].Path != "/ping" {
		t.Errorf("排序[0] = %+v; want gin GET /ping", res.Routes[0])
	}
	if res.Routes[1].Resolver != "gin" || res.Routes[1].Method != "POST" {
		t.Errorf("排序[1] = %+v; want gin POST /api/orders", res.Routes[1])
	}
	if res.Routes[2].Resolver != "native" || res.Routes[2].Path != "/" {
		t.Errorf("排序[2] = %+v; want native /", res.Routes[2])
	}
	// JSON 契约字段
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"routes"`, `"method"`, `"path"`, `"handler"`, `"resolver"`, `"register"`, `"POST"`, `"native"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("JSON 缺 %q:\n%s", want, b)
		}
	}
}
