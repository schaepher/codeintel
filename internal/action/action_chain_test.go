package action

// R95 测试（迁自 cli/query_r84_test.go + query_ext_chain_test.go）：
// chainSymbols（链上接口具体化）与 Actions.ExtChain（递归展开 + 缓存
// 失效）。渲染（writeExtChain 缩进树）留 cli 测试。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedExtChainRepo fixture：
//
//	daemon:(d).Run → 调 grpc 客户端 OrderServiceClient
//	OrderService 服务端 (orderServiceImpl).SubmitOrder → 调 http.Get（sms）
//	http_call 边：SubmitOrder → symbol:http:sms.example.com:route./send
func seedExtChainRepo(t *testing.T) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	if err := writeGoMod(dir); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		// daemon 调用方
		{ID: "symbol:go:example.com/m/daemon:(D).Run", Kind: domain.KindMethod, Name: "(D).Run", FilePath: "daemon/d.go", LineStart: 5},
		// grpc 客户端类型
		{ID: "symbol:go:example.com/m/pb:OrderServiceClient", Kind: domain.KindStruct, Name: "OrderServiceClient", FilePath: "pb/order_grpc.pb.go"},
		// grpc 服务 + 实现
		{ID: "symbol:go:example.com/m/pb:svc.OrderService", Kind: domain.KindGrpcService,
			Name: "svc.OrderService", FilePath: "pb/order_grpc.pb.go",
			Properties: map[string]any{"service_name": "OrderService", "methods": "SubmitOrder"}},
		{ID: "symbol:go:example.com/m/impl:orderServiceImpl", Kind: domain.KindStruct, Name: "orderServiceImpl", FilePath: "impl/order.go", LineStart: 10},
		{ID: "symbol:go:example.com/m/impl:(orderServiceImpl).SubmitOrder", Kind: domain.KindMethod,
			Name: "(orderServiceImpl).SubmitOrder", FilePath: "impl/order.go", LineStart: 12,
			Properties: map[string]any{"signature": "func (s *orderServiceImpl) SubmitOrder(r order.Request) (order.Resp, error)"}},
		// http_call 边 target 节点（外键约束——端点须存在否则边被丢弃）
		{ID: "symbol:http:sms.example.com:route./send", Kind: domain.KindHTTPRoute,
			Name: "route./send", FilePath: "impl/order.go",
			Properties: map[string]any{"path": "/send", "method": "GET"}},
	}, []*domain.Fact{
		// 客户端调用：daemon → OrderServiceClient
		{SourceID: "symbol:go:example.com/m/daemon:(D).Run", TargetID: "symbol:go:example.com/m/pb:OrderServiceClient",
			Kind: domain.FactCalls, Confidence: 0.9, Metadata: map[string]any{"line_num": 6}},
		// 服务端实现：实现类型 → 服务
		{SourceID: "symbol:go:example.com/m/impl:orderServiceImpl", TargetID: "symbol:go:example.com/m/pb:svc.OrderService",
			Kind: domain.FactGrpcImpl, Confidence: 1.0},
		// 服务端方法调 http（sms）
		{SourceID: "symbol:go:example.com/m/impl:(orderServiceImpl).SubmitOrder", TargetID: "symbol:http:sms.example.com:route./send",
			Kind: domain.FactHTTPCall, Confidence: 1.0, Metadata: map[string]any{
				"host": "sms.example.com", "path": "/send", "line_num": 14}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "b1", ToolName: "all", Status: "success", CommitSHA: "abc123"}); err != nil {
		t.Fatal(err)
	}
	return New(r), dir
}

// writeGoMod 写 fixture 仓库 go.mod（seed 前置）。
func writeGoMod(dir string) error {
	return os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644)
}

// TestExtChainRecursive：递归展开——daemon → OrderService（客户端）
// → 服务端方法 SubmitOrder → http sms。
func TestExtChainRecursive(t *testing.T) {
	acts, dir := seedExtChainRepo(t)
	root, err := acts.ExtChain(ExtChainRequest{
		Symbol: "symbol:go:example.com/m/daemon:(D).Run", RepoAbs: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(root.Grpc) != 1 || root.Grpc[0].Service != "OrderService" {
		t.Fatalf("grpc 列表 = %+v; want [OrderService]", root.Grpc)
	}
	if len(root.Grpc[0].Server) != 1 {
		t.Fatalf("服务端方法链 = %d; want 1（SubmitOrder）", len(root.Grpc[0].Server))
	}
	svr := root.Grpc[0].Server[0]
	if !strings.Contains(svr.Symbol, "SubmitOrder") {
		t.Errorf("服务端符号 = %s; want 含 SubmitOrder", svr.Symbol)
	}
	if len(svr.HTTP) != 1 || svr.HTTP[0].Service != "sms.example.com" {
		t.Errorf("服务端方法 http = %+v; want [sms.example.com]", svr.HTTP)
	}
}

// TestExtChainCache：缓存——第二次查询走数据库（删除索引后仍命中）；
// 索引 commit 变化 → 失效重算。
func TestExtChainCache(t *testing.T) {
	acts, dir := seedExtChainRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	sym := "symbol:go:example.com/m/daemon:(D).Run"
	// 第一次：计算 + 写缓存
	out1, err := acts.ChainGrpcHTTP(ChainGrpcHTTPRequest{Symbol: sym})
	if err != nil {
		t.Fatal(err)
	}
	if len(out1.Grpc) != 1 {
		t.Fatalf("第一次 grpc = %+v", out1.Grpc)
	}
	// 删除索引调用边（模拟索引被改）——缓存仍命中（build 未变）
	_, _ = r.Exec(`DELETE FROM edges WHERE source_id = ?`, sym)
	out2, err := acts.ChainGrpcHTTP(ChainGrpcHTTPRequest{Symbol: sym})
	if err != nil {
		t.Fatal(err)
	}
	if len(out2.Grpc) != 1 {
		t.Errorf("缓存命中失败（第二次 grpc = %+v）", out2.Grpc)
	}
	// 索引 commit 变化 → 缓存失效
	_, _ = r.Exec(`UPDATE build_metadata SET commit_sha = 'def456' WHERE build_id = 'b1'`)
	out3, err := acts.ChainGrpcHTTP(ChainGrpcHTTPRequest{Symbol: sym})
	if err != nil {
		t.Fatal(err)
	}
	if len(out3.Grpc) != 0 {
		t.Errorf("索引变化后应失效重算（grpc = %+v）", out3.Grpc)
	}
}

// TestChainSymbolsIfaceConcrete：BFS 链上接口方法/类型 → 经 implements
// 边具体化到实现（不停止解析——grpc 服务入口动态分派语义）。
func TestChainSymbolsIfaceConcrete(t *testing.T) {
	acts, dir := seedIfaceEntryRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	// 调用点：caller 调接口类型 + 接口方法（grpc handler 形态）
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:caller", Kind: domain.KindFunction, Name: "caller", FilePath: "main.go", LineStart: 1},
	}, []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:caller", TargetID: "symbol:go:example.com/m:Svc",
			Kind: domain.FactCalls, Confidence: 1.0},
		{SourceID: "symbol:go:example.com/m:caller", TargetID: "symbol:go:example.com/m:(Svc).Run",
			Kind: domain.FactCalls, Confidence: 1.0},
	}, nil); err != nil {
		t.Fatal(err)
	}
	seen := chainSymbols(acts, "symbol:go:example.com/m:caller")
	for _, want := range []string{"symbol:go:example.com/m:svcImpl", "symbol:go:example.com/m:(svcImpl).Run", "symbol:go:example.com/m:helper"} {
		if !seen[want] {
			t.Errorf("chainSymbols 链应含接口具体化 %q; got %v", want, seen)
		}
	}
	// Unimplemented 桩不入链
	if seen["symbol:go:example.com/m:UnimplementedSvc"] {
		t.Errorf("Unimplemented 桩不应入链: %v", seen)
	}
}
