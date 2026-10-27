package sqlite

// todo22 §4.1：RPC 转发/注册查询的仓储契约与索引计划。
//
// 约束（与 §7.2 同口径）：
//   - 查询按 kind 常量 / 表达式索引走索引，不做全库扫描；
//   - 客户端声明身份（rpc_client_service）与服务端注册身份（rpc_service）
//     分开保存——两者不同是"服务改名/配置不一致"，查询侧必须能发现；
//   - 多实现必须全部返回（调用方不得静默取第一个）。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

func TestRPCForwardQueriesAndPlan(t *testing.T) {
	r := newTestRepo(t)
	save(t, r, []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m/pb:svc.AccountService", Kind: domain.KindGrpcService,
			Name: "svc.AccountService", FilePath: "app.go", LineStart: 26,
			Properties: map[string]any{
				"service_name": "AccountService", "handler_name": "AccountService",
				"rpc_protocol": "micro", "rpc_service": "example.flow.AccountService",
				"rpc_client_service": "example.flow.AccountService", "methods": "Apply",
			}},
		{ID: "symbol:go:example.com/m/pb:svc.OtherService", Kind: domain.KindGrpcService,
			Name: "svc.OtherService", FilePath: "app.go", LineStart: 28,
			Properties: map[string]any{
				"service_name": "OtherService", "handler_name": "OtherService",
				"rpc_protocol": "micro", "rpc_service": "example.flow.OtherService",
				"rpc_client_service": "example.flow.OtherService", "methods": "Apply",
			}},
		{ID: "symbol:go:example.com/m:Stub", Kind: domain.KindMethod, Name: "(stub).Apply",
			FilePath: "pb.go", LineStart: 10},
		{ID: "symbol:go:example.com/m:AccountServer", Kind: domain.KindStruct, Name: "AccountServer",
			FilePath: "app.go", LineStart: 5},
		{ID: "symbol:go:example.com/m:OtherServer", Kind: domain.KindStruct, Name: "OtherServer",
			FilePath: "app.go", LineStart: 9},
	}, []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:Stub", TargetID: "symbol:go:example.com/m/pb:svc.AccountService",
			Kind: domain.FactGrpcCall, ToolSource: domain.ToolCodeGraph, Confidence: 1, Key: "Apply",
			Metadata: map[string]any{"line_num": 12, "pos": 345, "method": "Apply",
				"rpc_protocol": "micro", "rpc_service": "example.flow.AccountService"}},
		{SourceID: "symbol:go:example.com/m:AccountServer", TargetID: "symbol:go:example.com/m/pb:svc.AccountService",
			Kind: domain.FactGrpcImpl, ToolSource: domain.ToolCodeGraph, Confidence: 1},
		{SourceID: "symbol:go:example.com/m:OtherServer", TargetID: "symbol:go:example.com/m/pb:svc.OtherService",
			Kind: domain.FactGrpcImpl, ToolSource: domain.ToolCodeGraph, Confidence: 1},
		// 第二个实现（多实现必须全部返回）
		{SourceID: "symbol:go:example.com/m:OtherServer", TargetID: "symbol:go:example.com/m/pb:svc.AccountService",
			Kind: domain.FactGrpcImpl, ToolSource: domain.ToolCodeGraph, Confidence: 1},
	})

	fwds, err := r.GetRPCForwards("symbol:go:example.com/m:Stub")
	if err != nil {
		t.Fatalf("GetRPCForwards: %v", err)
	}
	if len(fwds) != 1 {
		t.Fatalf("RPC 出站边 = %d; want 1", len(fwds))
	}
	f := fwds[0]
	if f.Protocol != domain.RPCProtocolMicro || f.Method != "Apply" || f.Line != 12 || f.Pos != 345 {
		t.Errorf("转发字段 = %+v", f)
	}
	if f.FullIdentity != "example.flow.AccountService" || f.RegistrationIdentity != "example.flow.AccountService" {
		t.Errorf("身份字段 = client %q / register %q", f.FullIdentity, f.RegistrationIdentity)
	}

	// 完整身份精确匹配：只返回该服务，不返回同名 Handler 的其他服务
	svcs, err := r.GetRPCServicesByFullIdentity(domain.RPCProtocolMicro, "example.flow.OtherService")
	if err != nil || len(svcs) != 1 || string(svcs[0].ID) != "symbol:go:example.com/m/pb:svc.OtherService" {
		t.Fatalf("按完整身份查询 = %+v, %v", svcs, err)
	}
	// 协议过滤：native grpc 服务不在 micro 结果里
	if all, err := r.GetRPCServicesByProtocol(domain.RPCProtocolMicro); err != nil || len(all) != 2 {
		t.Fatalf("micro 服务数 = %d, %v; want 2", len(all), err)
	}

	impls, err := r.GetGrpcImplNodes("symbol:go:example.com/m/pb:svc.AccountService")
	if err != nil || len(impls) != 2 {
		t.Fatalf("多实现 = %d, %v; want 2（不得静默取第一个）", len(impls), err)
	}
	ids := []string{string(impls[0].ID), string(impls[1].ID)}
	if !strings.Contains(strings.Join(ids, ","), "AccountServer") ||
		!strings.Contains(strings.Join(ids, ","), "OtherServer") {
		t.Errorf("实现集合 = %v", ids)
	}

	// EXPLAIN：按完整身份查询走 kind 部分索引 + rpc_service 表达式索引
	rows, err := r.Query("EXPLAIN QUERY PLAN "+rpcServicesByFullSQL,
		string(domain.RPCProtocolGrpc), domain.RPCProtocolMicro, "example.flow.AccountService")
	if err != nil {
		t.Fatalf("EXPLAIN: %v", err)
	}
	defer rows.Close()
	var plans []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		plans = append(plans, detail)
	}
	joined := strings.Join(plans, " | ")
	if !strings.Contains(joined, "idx_nodes_rpc_service") {
		t.Errorf("完整身份查询未命中 rpc_service 表达式索引:\n%s", joined)
	}
	if strings.Contains(joined, "SCAN nodes") && !strings.Contains(joined, "USING") {
		t.Errorf("完整身份查询仍是全表扫描:\n%s", joined)
	}
	// 协议过滤（kind=grpc_service）走 kind 部分索引
	rows2, err := r.Query("EXPLAIN QUERY PLAN "+rpcServicesSQL,
		string(domain.RPCProtocolGrpc), domain.RPCProtocolMicro)
	if err != nil {
		t.Fatalf("EXPLAIN(protocol): %v", err)
	}
	defer rows2.Close()
	var plans2 []string
	for rows2.Next() {
		var id, parent, notused int
		var detail string
		if err := rows2.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		plans2 = append(plans2, detail)
	}
	joined2 := strings.Join(plans2, " | ")
	if !strings.Contains(joined2, "idx_nodes_grpc_service") {
		t.Errorf("协议过滤未命中 grpc_service 部分索引:\n%s", joined2)
	}
}
