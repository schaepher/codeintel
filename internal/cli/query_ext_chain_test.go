package cli

// R83/R95 测试：query ext-chain 命令端到端（递归链逻辑在 action——
// Actions.ExtChain 已单独测试；cli 只做参数转发 + 树状输出）。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedExtChainRepo fixture：
//
//	daemon:(d).Run → 调 grpc 客户端 OrderServiceClient
//	OrderService 服务端 (orderServiceImpl).SubmitOrder → 调 http.Get（sms）
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

// TestCmdExtChainForward：命令端到端——树状输出含 grpc 服务/服务端
// 方法/http host；JSON 输出含契约字段。
func TestCmdExtChainForward(t *testing.T) {
	dir := seedExtChainRepo(t)
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
	jout := captureStdout(func() {
		if code := cmdQuery([]string{"ext-chain", "symbol:go:example.com/m/daemon:(D).Run", "--repo", dir, "--json"}); code != 0 {
			t.Fatalf("ext-chain --json exit = %d", code)
		}
	})
	for _, want := range []string{`"symbol"`, `"grpc"`, `"server"`, `"http"`} {
		if !strings.Contains(jout, want) {
			t.Errorf("ext-chain JSON 应含 %s:\n%s", want, jout)
		}
	}
}
