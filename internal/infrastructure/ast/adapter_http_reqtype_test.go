package ast

// R100 待办11：http 请求对象判定——① gin 路由 handler 的请求对象
// （ShouldBind(&req) → 路由节点 req_types 属性）；② http_call 边请求体
// 类型（NewRequest/NewRequestWithContext 的 body 实参 → 边 req_type，
// 与 grpc 对齐——外部接口判定条件②）。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestHTTPRouteHandlerReqTypes：gin handler ShouldBind(&req) → 路由节点
// req_types（内联 FuncLit + 同包具名函数两种形态）。
func TestHTTPRouteHandlerReqTypes(t *testing.T) {
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

func New() *Engine { return nil }

func (e *Engine) GET(p string, h any) {}

func (e *Engine) POST(p string, h any) {}

func (c *Context) ShouldBind(obj any) error { return nil }
`,
		"api/routes.go": `package api

import "github.com/gin-gonic/gin"

type ChargeReq struct{}

type OrderReq struct{}

// 具名 handler：请求对象来自函数体 ShouldBind
func createOrder(c *gin.Context) {
	var req OrderReq
	c.ShouldBind(&req)
}

func setup() {
	r := gin.New()
	// 内联 FuncLit + ShouldBind
	r.POST("/charge", func(c *gin.Context) {
		var req ChargeReq
		c.ShouldBind(&req)
	})
	// 具名 handler
	r.POST("/orders", createOrder)
	// 无绑定 → req_types 空
	r.GET("/ping", func(c *gin.Context) {})
}
`,
	})
	reqTypes := map[string]string{}
	for _, n := range nodes {
		if n.Kind == domain.KindHTTPRoute {
			path := asStr(n.Properties["path"])
			reqTypes[path] = asStr(n.Properties["req_types"])
		}
	}
	if got := reqTypes["/charge"]; got != "example.com/mtest/api.ChargeReq" {
		t.Errorf("/charge req_types = %q; want example.com/mtest/api.ChargeReq（内联 FuncLit）", got)
	}
	if got := reqTypes["/orders"]; got != "example.com/mtest/api.OrderReq" {
		t.Errorf("/orders req_types = %q; want example.com/mtest/api.OrderReq（具名 handler）", got)
	}
	if got := reqTypes["/ping"]; got != "" {
		t.Errorf("/ping req_types = %q; want 空（无绑定）", got)
	}
}

// TestHTTPCallEdgeReqType：http.NewRequest 的 body 实参类型 → http_call
// 边 req_type（Get 无 body → 空）。
func TestHTTPCallEdgeReqType(t *testing.T) {
	_, facts := indexFixture(t, map[string]string{
		"go.mod": "module example.com/mtest\n\ngo 1.21\n",
		"http/http.go": `package http

type Request struct{}

func Get(url string) {}

func NewRequest(method, url string, body any) *Request { return nil }

func (r *Request) Do() {}
`,
		"svc_a/client.go": `package svc_a

import "example.com/mtest/http"

type ChargeBody struct{}

func callNewReq(body *ChargeBody) {
	http.NewRequest("POST", "https://api.ext.com/charge", body)
}

func callGet() {
	http.Get("https://api.ext.com/ping")
}
`,
	})
	reqTypes := map[string]string{}
	for _, f := range facts {
		if f.Kind == domain.FactHTTPCall {
			src := string(f.SourceID)
			fn := src[strings.LastIndex(src, ":")+1:]
			if v, ok := f.Metadata["req_type"].(string); ok {
				reqTypes[fn] = v
			}
		}
	}
	if got := reqTypes["callNewReq"]; got != "example.com/mtest/svc_a.ChargeBody" {
		t.Errorf("NewRequest req_type = %q; want example.com/mtest/svc_a.ChargeBody（body 实参类型）", got)
	}
	if got := reqTypes["callGet"]; got != "" {
		t.Errorf("http.Get req_type = %q; want 空（无请求体）", got)
	}
}
