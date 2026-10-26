//go:build integration

package integration

// todo12-16 A1/A2：多模块 + 外部生成代码（替身）+ 自定义注册 + DI 客户端。
//   - grpc-routes 识别两个服务；grpc_call 逐方法（同名 Submit 不串线）
//   - ext-chain：客户端包装方法 → OrderService 实现 Submit → HTTP 下游
//   - 非 RPC 同名函数不建 grpc_call

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
)

const grpcGenGoMod = `module example.com/gen

go 1.21
`

const grpcStub = `package grpcstub

type ServiceDesc struct{ ServiceName string }

type ServiceRegistrar interface {
	RegisterService(desc *ServiceDesc, impl any)
}
`

const grpcGenOrder = `package gen

import (
	"context"

	"example.com/gen/grpcstub"
)

type SubmitRequest struct{ ID string }
type SubmitResponse struct{ OK bool }

type OrderServiceClient interface {
	Submit(ctx context.Context, in *SubmitRequest, opts ...any) (*SubmitResponse, error)
}

type orderClient struct{}

func (c *orderClient) Submit(ctx context.Context, in *SubmitRequest, opts ...any) (*SubmitResponse, error) {
	return &SubmitResponse{OK: true}, nil
}

func NewOrderServiceClient() OrderServiceClient { return &orderClient{} }

type OrderServiceServer interface {
	Submit(ctx context.Context, in *SubmitRequest) (*SubmitResponse, error)
}

func RegisterOrderServiceServer(s grpcstub.ServiceRegistrar, srv OrderServiceServer) {
	s.RegisterService(&grpcstub.ServiceDesc{ServiceName: "app.OrderService"}, srv)
}
`

const grpcGenPayment = `package gen

import (
	"context"

	"example.com/gen/grpcstub"
)

type PayRequest struct{ ID string }
type PayResponse struct{ OK bool }

type PaymentServiceClient interface {
	Submit(ctx context.Context, in *PayRequest, opts ...any) (*PayResponse, error)
}

type paymentClient struct{}

func (c *paymentClient) Submit(ctx context.Context, in *PayRequest, opts ...any) (*PayResponse, error) {
	return &PayResponse{OK: true}, nil
}

type PaymentServiceServer interface {
	Submit(ctx context.Context, in *PayRequest) (*PayResponse, error)
}

func RegisterPaymentServiceServer(s grpcstub.ServiceRegistrar, srv PaymentServiceServer) {
	s.RegisterService(&grpcstub.ServiceDesc{ServiceName: "app.PaymentService"}, srv)
}
`

func TestTodo12GrpcMultiServiceRealIndex(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", `module example.com/app

go 1.21

require example.com/gen v0.0.0

replace example.com/gen => ./gen
`)
	write("gen/go.mod", grpcGenGoMod)
	write("gen/grpcstub/stub.go", grpcStub)
	write("gen/order.pb.go", grpcGenOrder)
	write("gen/payment.pb.go", grpcGenPayment)
	write("gateway/gateway.go", `package gateway

import (
	"context"

	"example.com/gen"
)

type Gateway struct {
	order   gen.OrderServiceClient
	payment gen.PaymentServiceClient
}

// DI/构造器注入：不经过 NewXxxClient 变量追踪
func NewGateway(o gen.OrderServiceClient, p gen.PaymentServiceClient) *Gateway {
	return &Gateway{order: o, payment: p}
}

func (g *Gateway) SubmitOrder(ctx context.Context, req *gen.SubmitRequest) (*gen.SubmitResponse, error) {
	return g.order.Submit(ctx, req)
}

func (g *Gateway) SubmitPayment(ctx context.Context, req *gen.PayRequest) (*gen.PayResponse, error) {
	return g.payment.Submit(ctx, req)
}
`)
	write("server/server.go", `package server

import (
	"context"
	"net/http"

	"example.com/gen"
	"example.com/gen/grpcstub"
)

type OrderImpl struct{}

func (o *OrderImpl) Submit(ctx context.Context, req *gen.SubmitRequest) (*gen.SubmitResponse, error) {
	_, _ = http.Get("https://notify.example.test/send")
	return &gen.SubmitResponse{OK: true}, nil
}

type PaymentImpl struct{}

func (p *PaymentImpl) Submit(ctx context.Context, req *gen.PayRequest) (*gen.PayResponse, error) {
	return &gen.PayResponse{OK: true}, nil
}

// 自定义注册函数（名称非 RegisterXxx，签名有 RegisterService 调用）
func SetupOrder(reg grpcstub.ServiceRegistrar) {
	gen.RegisterOrderServiceServer(reg, &OrderImpl{})
}

func SetupPayment(reg grpcstub.ServiceRegistrar) {
	gen.RegisterPaymentServiceServer(reg, &PaymentImpl{})
}

// 非 RPC 同名函数：不得生成 grpc_call
func Submit() {}
`)
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}

	// grpc-routes：两个服务 + 各自 Submit 方法
	code, out := runCLIOut(t, "query", "grpc-routes", "--repo", dir, "--json")
	if code != 0 {
		t.Fatalf("grpc-routes exit = %d; %s", code, out)
	}
	var routes map[string]any
	if err := json.Unmarshal([]byte(out), &routes); err != nil {
		t.Fatalf("grpc-routes JSON: %v\n%s", err, out)
	}
	svcs, _ := routes["services"].([]any)
	names := map[string]bool{}
	for _, s := range svcs {
		if m, ok := s.(map[string]any); ok {
			if n, _ := m["name"].(string); n != "" {
				names[n] = true
			}
		}
	}
	if !names["OrderService"] || !names["PaymentService"] {
		t.Fatalf("应识别两个服务: %v\n%s", names, out)
	}

	// ext-chain：Gateway.SubmitOrder → OrderService 实现 Submit → HTTP 下游
	sym := "symbol:go:example.com/app/gateway:(Gateway).SubmitOrder"
	code, out = runCLIOut(t, "query", "ext-chain", sym, "--repo", dir, "--json")
	if code != 0 {
		t.Fatalf("ext-chain exit = %d; %s", code, out)
	}
	var root action.ExtChainNode
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("ext-chain JSON: %v\n%s", err, out)
	}
	if len(root.Grpc) != 1 {
		t.Fatalf("SubmitOrder 应有 1 条 gRPC 出站: %+v", root.Grpc)
	}
	g := root.Grpc[0]
	if g.Method != "Submit" || g.Stop != nil {
		t.Fatalf("Submit 应精确续接: %+v", g)
	}
	if !strings.Contains(g.TargetMethod, "OrderImpl") {
		t.Errorf("目标应为 OrderImpl.Submit（服务身份不串线）: %+v", g)
	}
	hasNotify := false
	for _, s := range g.Server {
		for _, h := range s.HTTP {
			if h.Service == "notify.example.test" {
				hasNotify = true
			}
		}
	}
	if !hasNotify {
		t.Errorf("服务端 Submit 的 HTTP 下游应可见: %+v", g.Server)
	}

	// Payment 同名 Submit 不得串到 Order 实现
	sym2 := "symbol:go:example.com/app/gateway:(Gateway).SubmitPayment"
	code, out = runCLIOut(t, "query", "ext-chain", sym2, "--repo", dir, "--json")
	if code != 0 {
		t.Fatalf("ext-chain(payment) exit = %d; %s", code, out)
	}
	var root2 action.ExtChainNode
	if err := json.Unmarshal([]byte(out), &root2); err != nil {
		t.Fatalf("ext-chain(payment) JSON: %v", err)
	}
	if len(root2.Grpc) != 1 || !strings.Contains(root2.Grpc[0].TargetMethod, "PaymentImpl") {
		t.Fatalf("Payment 同名 Submit 应续接到 PaymentImpl: %+v", root2.Grpc)
	}
	for _, s := range root2.Grpc[0].Server {
		for _, h := range s.HTTP {
			if h.Service == "notify.example.test" {
				t.Errorf("Payment 不得串到 Order 的 HTTP 下游: %+v", s.HTTP)
			}
		}
	}
}
