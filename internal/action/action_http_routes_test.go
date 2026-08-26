package action

// R92 测试（迁自 cli/query_http_routes_test.go）：Actions.HTTPRoutes
// ——http_route 节点（构建期两个 resolver 发射）→ 契约 JSON。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedHTTPRoutesRepo 构造含 http_route 节点的索引（native + gin 混合）。
func seedHTTPRoutesRepo(t *testing.T) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
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
	return New(r), dir
}

// TestQueryHTTPRoutes：JSON 契约——method/path/handler/resolver/register。
func TestQueryHTTPRoutes(t *testing.T) {
	acts, _ := seedHTTPRoutesRepo(t)
	res, err := acts.HTTPRoutes()
	if err != nil {
		t.Fatalf("HTTPRoutes: %v", err)
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

// TestQueryHTTPRoutesHandlerID：handler_id 契约字段（R37）——发射端存
// canonical ID；老索引无该属性（NULL）读取为空且不丢行。
func TestQueryHTTPRoutesHandlerID(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m/api:route.1", Kind: domain.KindHTTPRoute,
			Name: "GET /ping", Properties: map[string]any{
				"method": "GET", "path": "/ping", "handler": "pingHandler",
				"handler_id": "symbol:go:example.com/m/api:pingHandler",
				"resolver":   "gin", "register": "api/routes.go:20",
			}},
		{ID: "symbol:go:example.com/m/api:route.2", Kind: domain.KindHTTPRoute,
			Name: " /", Properties: map[string]any{
				"method": "", "path": "/", "handler": "home",
				"resolver": "native", "register": "api/api.go:12",
			}},
	}
	if _, err := r.SaveBatchStats(nodes, nil, nil); err != nil {
		t.Fatalf("save nodes: %v", err)
	}
	acts := New(r)
	res, err := acts.HTTPRoutes()
	if err != nil {
		t.Fatalf("HTTPRoutes: %v", err)
	}
	if len(res.Routes) != 2 {
		t.Fatalf("路由数 = %d; want 2（老索引 NULL 路由不应丢行）:\n%+v", len(res.Routes), res)
	}
	for _, rte := range res.Routes {
		if rte.Path == "/ping" && rte.HandlerID != "symbol:go:example.com/m/api:pingHandler" {
			t.Errorf("/ping handler_id = %q; want 发射端 canonical ID", rte.HandlerID)
		}
		if rte.Path == "/" && rte.HandlerID != "" {
			t.Errorf("/ handler_id = %q; want 空（老索引无属性）", rte.HandlerID)
		}
	}
}
