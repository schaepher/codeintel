package action

// R94 测试（迁自 cli/query_external_interfaces_test.go）：
// Actions.ExternalInterfaces ——外部接口识别——grpc 目标服务无本项目
// 定义特征（注册/方法）+ 请求类型不在服务参数集合；http 目标路由无
// handler。内部调用排除。

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedExternalInterfacesRepo 外部接口 fixture：
//   - 本项目 grpc 服务（methods/param_types）——内部调用应排除
//   - 外部 grpc 服务（只有 service_name）——内部调用应排除（无 param
//     匹配的会命中 req_type 条件排除）
//   - 外部 grpc 调用（req_type 不在本项目参数集合）→ 应识别
//   - 本项目 http 路由（有 handler + req_types）——调用应排除
//   - 外部 http（无 handler 的路由节点）→ 应识别；带 req_type 的按
//     请求对象判定（R100：∉ 本地路由请求类型 → 确认；∈ → 排除）
func seedExternalInterfacesRepo(t *testing.T) *Actions {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		// 本项目 grpc 服务（接口签名识别：methods + param_types）
		{ID: "symbol:go:example.com/m/grpc:svc.QueryService", Kind: domain.KindGrpcService,
			Name: "svc.QueryService", FilePath: "grpc/query.go",
			Properties: map[string]any{
				"service_name": "QueryService",
				"methods":      "Get,List",
				"param_types":  "example.com/m/api.GetRequest,example.com/m/api.ListRequest",
			}},
		// 外部 grpc 服务（外部调用创建——只有 service_name）
		{ID: "symbol:go:example.com/ext/pay:svc.PayService", Kind: domain.KindGrpcService,
			Name:       "svc.PayService",
			Properties: map[string]any{"service_name": "PayService"}},
		// 本项目 http 路由（handler + req_types——请求对象判定本地集合）
		{ID: "symbol:go:example.com/m/grpc:route.1", Kind: domain.KindHTTPRoute,
			Name: "POST /get", FilePath: "grpc/query.go",
			Properties: map[string]any{"path": "/get", "method": "POST", "handler": "getHandler",
				"req_types": "example.com/m/api.GetRequest"}},
		// 外部 http 路由（外部 URL 创建——无 handler）
		{ID: "symbol:http:api.ext-pay.com:route./charge", Kind: domain.KindHTTPRoute,
			Name: "route./charge", Properties: map[string]any{"path": "/charge", "method": "POST"}},
		{ID: "symbol:http:api.ext-pay.com:route./callback", Kind: domain.KindHTTPRoute,
			Name: "route./callback", Properties: map[string]any{"path": "/callback", "method": "POST"}},
		{ID: "symbol:http:api.ext-pay.com:route./internal", Kind: domain.KindHTTPRoute,
			Name: "route./internal", Properties: map[string]any{"path": "/internal", "method": "POST"}},
		// 调用函数
		{ID: "symbol:go:example.com/m/app:doPay", Kind: domain.KindFunction,
			Name: "doPay", FilePath: "app/pay.go", LineStart: 10},
		{ID: "symbol:go:example.com/m/app:query", Kind: domain.KindFunction,
			Name: "query", FilePath: "app/query.go", LineStart: 20},
	}
	if _, err := r.SaveBatchStats(nodes, nil, nil); err != nil {
		t.Fatalf("save nodes: %v", err)
	}
	facts := []*domain.Fact{
		// 外部 grpc 调用：req_type 不在本项目参数集合
		{SourceID: "symbol:go:example.com/m/app:doPay", TargetID: "symbol:go:example.com/ext/pay:svc.PayService",
			Kind: domain.FactGrpcCall, Confidence: 1.0,
			Metadata: map[string]any{"method": "Charge", "req_type": "example.com/ext/pay.ChargeRequest", "line_num": 11}},
		// 内部 grpc 调用：本项目服务（registers/methods 特征）——排除
		{SourceID: "symbol:go:example.com/m/app:query", TargetID: "symbol:go:example.com/m/grpc:svc.QueryService",
			Kind: domain.FactGrpcCall, Confidence: 1.0,
			Metadata: map[string]any{"method": "Get", "req_type": "example.com/m/api.GetRequest", "line_num": 21}},
		// 外部 http 调用：无 handler 路由 + 无 req_type
		{SourceID: "symbol:go:example.com/m/app:doPay", TargetID: "symbol:http:api.ext-pay.com:route./charge",
			Kind: domain.FactHTTPCall, Confidence: 1.0,
			Metadata: map[string]any{"url": "https://api.ext-pay.com/charge", "host": "api.ext-pay.com", "method": "POST", "line_num": 12}},
		// 外部 http 调用：请求对象不在本地路由请求类型 → 确认 + ReqType
		{SourceID: "symbol:go:example.com/m/app:doPay", TargetID: "symbol:http:api.ext-pay.com:route./callback",
			Kind: domain.FactHTTPCall, Confidence: 1.0,
			Metadata: map[string]any{"url": "https://api.ext-pay.com/callback", "host": "api.ext-pay.com", "method": "POST",
				"req_type": "example.com/ext/pay.ChargeRequest", "line_num": 13}},
		// 外部 http 调用：请求对象 ∈ 本地路由请求类型 → 排除（条件②）
		{SourceID: "symbol:go:example.com/m/app:query", TargetID: "symbol:http:api.ext-pay.com:route./internal",
			Kind: domain.FactHTTPCall, Confidence: 1.0,
			Metadata: map[string]any{"url": "https://api.ext-pay.com/internal", "host": "api.ext-pay.com", "method": "POST",
				"req_type": "example.com/m/api.GetRequest", "line_num": 22}},
	}
	if _, err := r.SaveBatchStats(nil, facts, nil); err != nil {
		t.Fatalf("save facts: %v", err)
	}
	return New(r)
}

// TestExternalInterfaces：外部接口识别——外部 grpc（PayService.Charge）
// 与外部 http（api.ext-pay.com/charge）识别；内部 grpc（QueryService.
// Get）排除。
func TestExternalInterfaces(t *testing.T) {
	acts := seedExternalInterfacesRepo(t)
	res, err := acts.ExternalInterfaces()
	if err != nil {
		t.Fatalf("ExternalInterfaces: %v", err)
	}
	got := map[string]bool{}
	reqTypeOf := map[string]string{}
	for _, ei := range res.Interfaces {
		got[ei.Kind+"|"+ei.Service+"|"+ei.Method] = true
		reqTypeOf[ei.Kind+"|"+ei.Service+"|"+ei.Method] = ei.ReqType
	}
	if !got["grpc|PayService|Charge"] {
		t.Errorf("应识别外部 grpc PayService.Charge:\n%+v", res.Interfaces)
	}
	if !got["http|api.ext-pay.com|POST /charge"] {
		t.Errorf("应识别外部 http api.ext-pay.com/charge:\n%+v", res.Interfaces)
	}
	if !got["http|api.ext-pay.com|POST /callback"] {
		t.Errorf("应识别外部 http /callback（请求对象 ∉ 本地集合）:\n%+v", res.Interfaces)
	}
	if got["http|api.ext-pay.com|POST /internal"] {
		t.Error("外部 http /internal 请求对象 ∈ 本地路由请求类型——不应识别")
	}
	if got["grpc|QueryService|Get"] {
		t.Error("内部 grpc 调用（本项目服务）不应识别为外部")
	}
	// http 请求对象判定（R100）：ReqType = 调用请求体类型
	if reqTypeOf["http|api.ext-pay.com|POST /callback"] != "example.com/ext/pay.ChargeRequest" {
		t.Errorf("http ReqType = %q; want example.com/ext/pay.ChargeRequest（请求对象判定）",
			reqTypeOf["http|api.ext-pay.com|POST /callback"])
	}
	// 外部 grpc 带请求类型
	for _, ei := range res.Interfaces {
		if ei.Kind == "grpc" && ei.Service == "PayService" {
			if ei.ReqType != "example.com/ext/pay.ChargeRequest" {
				t.Errorf("req_type = %q; want 外部请求类型", ei.ReqType)
			}
			if len(ei.Callers) == 0 || ei.Callers[0].Func != "doPay" {
				t.Errorf("callers 应含 doPay: %+v", ei.Callers)
			}
			if ei.Callers[0].Loc != "app/pay.go:11" {
				t.Errorf("callers[0].Loc = %q; want app/pay.go:11（调用行）", ei.Callers[0].Loc)
			}
			if ei.Callers[0].Pkg != "example.com/m/app" {
				t.Errorf("callers[0].Pkg = %q; want example.com/m/app（架构图领域聚合）", ei.Callers[0].Pkg)
			}
		}
	}
}
