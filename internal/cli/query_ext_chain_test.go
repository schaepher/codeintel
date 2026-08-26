package cli

// R83 测试：query ext-chain 外部系统调用链——递归（grpc 客户端 →
// 服务端方法 → 服务端方法再调 http）+ 缓存（第二次走数据库）。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedExtChainRepo fixture：
//   daemon:(d).Run → 调 grpc 客户端 OrderServiceClient
//   OrderService 服务端 (orderServiceImpl).SubmitOrder → 调 http.Get（sms）
//   http_call 边：SubmitOrder → symbol:http:sms.example.com:route./send
func seedExtChainRepo(t *testing.T) string {
	t.Helper()
	dir := seedRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
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
	return dir
}

// TestExtChainRecursive：递归展开——daemon → OrderService（客户端）
// → 服务端方法 SubmitOrder → http sms。
func TestExtChainRecursive(t *testing.T) {
	dir := seedExtChainRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	acts, err2 := newTestActions(t, dir)
	if err2 != nil {
		t.Fatal(err2)
	}
	root := extChain(acts, r, dir, "symbol:go:example.com/m/daemon:(D).Run", map[string]bool{}, 0)
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
	// 命令端到端
	out := captureStdout(func() {
		if code := cmdQuery([]string{"ext-chain", "symbol:go:example.com/m/daemon:(D).Run", "--repo", dir}); code != 0 {
			t.Fatalf("ext-chain exit = %d", code)
		}
	})
	for _, want := range []string{"OrderService", "SubmitOrder", "sms.example.com"} {
		if !strings.Contains(out, want) {
			t.Errorf("ext-chain 输出应含 %q:\n%s", want, out)
		}
	}
}

// TestExtChainCache：缓存——第二次查询走数据库（删除索引后仍命中）。
func TestExtChainCache(t *testing.T) {
	dir := seedExtChainRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	acts, _ := newTestActions(t, dir)
	sym := "symbol:go:example.com/m/daemon:(D).Run"
	// 第一次：计算 + 写缓存
	out1 := chainGrpcHTTPCached(acts, r, sym)
	if len(out1.Grpc) != 1 {
		t.Fatalf("第一次 grpc = %+v", out1.Grpc)
	}
	// 删除索引调用边（模拟索引被改）——缓存仍命中（build 未变）
	_, _ = r.Exec(`DELETE FROM edges WHERE source_id = ?`, sym)
	out2 := chainGrpcHTTPCached(acts, r, sym)
	if len(out2.Grpc) != 1 {
		t.Errorf("缓存命中失败（第二次 grpc = %+v）", out2.Grpc)
	}
	// 索引 commit 变化 → 缓存失效
	_, _ = r.Exec(`UPDATE build_metadata SET commit_sha = 'def456' WHERE build_id = 'b1'`)
	out3 := chainGrpcHTTPCached(acts, r, sym)
	if len(out3.Grpc) != 0 {
		t.Errorf("索引变化后应失效重算（grpc = %+v）", out3.Grpc)
	}
}

