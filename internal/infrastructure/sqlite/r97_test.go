package sqlite

// R97 测试：接口方法具体化优先非 grpc 实现（grpc 实现内部调接口时
// 避免具体化回自身——时序图自环根因）。

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestInterfaceMethodImplPrefersBizImpl：接口 IManager 有多个实现——
// orderManagerImpl（业务）与 orderServiceImpl（grpc 实现，grpc_impl
// 边 source）——具体化应返回业务实现。
func TestInterfaceMethodImplPrefersBizImpl(t *testing.T) {
	r := newTestRepo(t)
	// 接口 + 两个实现 + 方法
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:m:IManager", Kind: domain.KindInterface, Name: "IManager"},
		{ID: "symbol:go:m/order:orderManagerImpl", Kind: domain.KindStruct, Name: "orderManagerImpl"},
		{ID: "symbol:go:m/order:(orderManagerImpl).SubmitOrder", Kind: domain.KindMethod, Name: "(orderManagerImpl).SubmitOrder"},
		{ID: "symbol:go:m/svc:orderServiceImpl", Kind: domain.KindStruct, Name: "orderServiceImpl"},
		{ID: "symbol:go:m/svc:(orderServiceImpl).SubmitOrder", Kind: domain.KindMethod, Name: "(orderServiceImpl).SubmitOrder"},
		{ID: "symbol:go:m/pb:svc.OrderService", Kind: domain.KindGrpcService, Name: "svc.OrderService",
			Properties: map[string]any{"service_name": "OrderService"}},
	}
	edges := []*domain.Fact{
		// 接口 → 两个实现（implements）
		{SourceID: "symbol:go:m:IManager", TargetID: "symbol:go:m/order:orderManagerImpl",
			Kind: domain.FactImplements, Confidence: 1.0},
		{SourceID: "symbol:go:m:IManager", TargetID: "symbol:go:m/svc:orderServiceImpl",
			Kind: domain.FactImplements, Confidence: 1.0},
		// orderServiceImpl 是 grpc 实现（grpc_impl 边 source）
		{SourceID: "symbol:go:m/svc:orderServiceImpl", TargetID: "symbol:go:m/pb:svc.OrderService",
			Kind: domain.FactGrpcImpl, Confidence: 1.0},
	}
	save(t, r, nodes, edges)

	got, ok := r.InterfaceMethodImpl("symbol:go:m:(IManager).SubmitOrder")
	if !ok {
		t.Fatal("应能具体化")
	}
	if got != "symbol:go:m/order:(orderManagerImpl).SubmitOrder" {
		t.Errorf("具体化 = %s; want 业务实现 orderManagerImpl（grpc 实现排后）", got)
	}
	// 形态 2：接口类型 → 实现类型（业务优先）
	got2, ok := r.InterfaceMethodImpl("symbol:go:m:IManager")
	if !ok || got2 != "symbol:go:m/order:orderManagerImpl" {
		t.Errorf("类型形态 = %s, %v; want orderManagerImpl", got2, ok)
	}
}
