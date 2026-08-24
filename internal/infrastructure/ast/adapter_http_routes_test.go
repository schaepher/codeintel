package ast

// R31 HTTP 路由识别测试：两个 resolver 各自识别模式——原生 net/http
// （包级 Handle/HandleFunc + ServeMux 方法调用）+ gin（*gin.Engine/
// *gin.RouterGroup 路由方法 + Group 前缀拼接）。测试先行。

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// routePropsOf 收集 http_route 节点属性（path → method,resolver,handler）。
func routePropsOf(nodes []*domain.CodeEntity) map[string][]string {
	out := map[string][]string{}
	for _, n := range nodes {
		if n.Kind != domain.KindHTTPRoute {
			continue
		}
		p := n.Properties
		path, _ := p["path"].(string)
		out[path] = []string{
			asStr(p["method"]), asStr(p["resolver"]), asStr(p["handler"]),
		}
	}
	return out
}

func asStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// TestHTTPRouteNative：原生 resolver——http.HandleFunc 包级 + ServeMux
// 方法调用（mux.HandleFunc/mux.Handle）→ http_route 节点（method 空、
// resolver=native、handler 名）。
func TestHTTPRouteNative(t *testing.T) {
	nodes, _ := indexFixture(t, map[string]string{
		"go.mod": "module example.com/mtest\n\ngo 1.21\n",
		"api/api.go": `package api

import "net/http"

func home(w http.ResponseWriter, r *http.Request) {}

type srv struct{}

func (s *srv) orders(w http.ResponseWriter, r *http.Request) {}

func setup() {
	http.HandleFunc("/", home)
	http.Handle("/health", http.HandlerFunc(home))
	mux := http.NewServeMux()
	mux.HandleFunc("/api/orders", home)
	mux.Handle("/assets", home)
	// 方法值 handler（R31 实测遗漏形态——ana serve 的 s.handleRoots）
	s := &srv{}
	mux.HandleFunc("/api/users/", s.orders)
}
`,
	})
	routes := routePropsOf(nodes)
	want := map[string][]string{
		"/":            {"", "native", "home"},
		"/health":      {"", "native", "home"},
		"/api/orders":  {"", "native", "home"},
		"/assets":      {"", "native", "home"},
		"/api/users/":  {"", "native", "s.orders"},
	}
	for path, props := range want {
		got, ok := routes[path]
		if !ok {
			t.Errorf("缺路由 %s（原生 resolver 未识别），全部: %v", path, routes)
			continue
		}
		if got[0] != props[0] || got[1] != props[1] || got[2] != props[2] {
			t.Errorf("路由 %s = %v; want %v", path, got, props)
		}
	}
}

// TestHTTPRouteGin：gin resolver——gin.New() 的 GET + Group 变量继承
// （g := r.Group("/api") 前缀拼接）+ 链式（r.Group("/v1").GET）→
// http_route 节点（method=GET/POST、resolver=gin、path 含前缀）。
func TestHTTPRouteGin(t *testing.T) {
	nodes, _ := indexFixture(t, map[string]string{
		"go.mod": `module example.com/mtest

go 1.21

require github.com/gin-gonic/gin v1.9.0

replace github.com/gin-gonic/gin => ./ginstub
`,
		"ginstub/go.mod": "module github.com/gin-gonic/gin\n\ngo 1.21\n",
		"ginstub/gin.go": `package gin

type Context struct{}

type Engine struct{}

type RouterGroup struct{}

func New() *Engine { return nil }

func (e *Engine) Group(p string) *RouterGroup { return nil }

func (g *RouterGroup) Group(p string) *RouterGroup { return nil }

func (e *Engine) GET(p string, h any) {}

func (g *RouterGroup) GET(p string, h any) {}

func (g *RouterGroup) POST(p string, h any) {}

func (g *RouterGroup) Handle(method string, p string, h ...any) {}

func (e *Engine) Handle(method string, p string, h ...any) {}

func (e *Engine) Static(p string, dir string) {}
`,
		"api/routes.go": `package api

import "github.com/gin-gonic/gin"

func pingHandler(c *gin.Context) {}

func listOrders(c *gin.Context) {}

func createOrder(c *gin.Context) {}

func healthHandler(c *gin.Context) {}

func deleteItem(c *gin.Context) {}

func logMw(c *gin.Context) {}

func setup() {
	r := gin.New()
	r.GET("/ping", pingHandler)
	g := r.Group("/api")
	g.GET("/orders", listOrders)
	g.POST("/orders", createOrder)
	r.Group("/v1").GET("/health", healthHandler)
	r.Static("/static", "./assets")
	// R31 补遗：Handle 通用注册 + 多 handler（中间件在前）+ 匿名函数
	r.Handle("DELETE", "/items/{id}", deleteItem)
	g.GET("/items", logMw, listOrders)
	r.GET("/anon", func(c *gin.Context) {})
}
`,
		// deleteItem 定义补上
	})
	routes := routePropsOf(nodes)
	want := map[string][]string{
		"/ping":      {"GET", "gin", "pingHandler"},
		"/v1/health": {"GET", "gin", "healthHandler"},
	}
	// GET/POST 同路径 /api/orders——routePropsOf 按 path 索引会覆盖，
	// 单独按 method 检查
	getSeen, postSeen := false, false
	for _, n := range nodes {
		if n.Kind == domain.KindHTTPRoute && asStr(n.Properties["path"]) == "/api/orders" {
			switch asStr(n.Properties["method"]) {
			case "GET":
				getSeen = true
			case "POST":
				postSeen = true
			}
		}
	}
	for path, props := range want {
		got, ok := routes[path]
		if !ok {
			t.Errorf("缺路由 %s（gin resolver 未识别），全部: %v", path, routes)
			continue
		}
		if got[0] != props[0] || got[1] != props[1] || got[2] != props[2] {
			t.Errorf("路由 %s = %v; want %v", path, got, props)
		}
	}
	if !getSeen {
		t.Error("缺 GET /api/orders（Group 变量继承前缀未生效）")
	}
	if !postSeen {
		t.Error("缺 POST /api/orders（Group 内 POST 注册）")
	}
	// R31 补遗：Handle 通用注册（method 从 args[0]）+ 多 handler（中间件
	// 在前，取最后一个为业务 handler）+ 匿名函数 handler
	routes = routePropsOf(nodes)
	if got, ok := routes["/items/{id}"]; !ok || got[0] != "DELETE" || got[1] != "gin" || got[2] != "deleteItem" {
		t.Errorf("Handle 通用注册 = %v; want [DELETE gin deleteItem]（全部: %v）", got, routes)
	}
	if got, ok := routes["/api/items"]; !ok || got[0] != "GET" || got[2] != "listOrders" {
		t.Errorf("多 handler（中间件+业务） = %v; want GET listOrders（取最后一个）", got)
	}
	if got, ok := routes["/anon"]; !ok || got[2] != "(匿名)" {
		t.Errorf("匿名函数 handler = %v; want (匿名)（不丢路由）", got)
	}
	// Static 系列不算（噪音）
	if _, ok := routes["/static"]; ok {
		t.Error("Static 静态资源不应进路由清单")
	}
}
