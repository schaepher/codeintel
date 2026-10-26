package ast

// todo12-16：客户端包装/DI/结构体字段调用（不依赖 NewXxxClient 构造器
// 变量追踪）——按方法接收者类型识别 gRPC 客户端方法；多服务同名方法不串线。
// 生成代码形态独立包（外部模块在集成测试覆盖）。

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

func TestGrpcClientByReceiverTypeDI(t *testing.T) {
	_, facts := indexFixture(t, map[string]string{
		"go.mod": "module example.com/mtest\n\ngo 1.21\n",
		"api/order.pb.go": `package api

import "context"

type SubmitRequest struct{}
type SubmitResponse struct{}

type OrderServiceClient interface {
	Submit(ctx context.Context, in *SubmitRequest, opts ...any) (*SubmitResponse, error)
}

type orderClient struct{}

func (c *orderClient) Submit(ctx context.Context, in *SubmitRequest, opts ...any) (*SubmitResponse, error) {
	return nil, nil
}

func NewOrderServiceClient() OrderServiceClient { return &orderClient{} }
`,
		"api/payment.pb.go": `package api

import "context"

type PayRequest struct{}
type PayResponse struct{}

type PaymentServiceClient interface {
	Submit(ctx context.Context, in *PayRequest, opts ...any) (*PayResponse, error)
}

type paymentClient struct{}

func (c *paymentClient) Submit(ctx context.Context, in *PayRequest, opts ...any) (*PayResponse, error) {
	return nil, nil
}
`,
		"gateway/gateway.go": `package gateway

import (
	"context"

	"example.com/mtest/api"
)

type Gateway struct {
	client  api.OrderServiceClient
	payment api.PaymentServiceClient
}

func NewGateway(c api.OrderServiceClient, p api.PaymentServiceClient) *Gateway {
	return &Gateway{client: c, payment: p}
}

func (g *Gateway) Submit(ctx context.Context, req *api.SubmitRequest) (*api.SubmitResponse, error) {
	return g.client.Submit(ctx, req)
}

func (g *Gateway) Pay(ctx context.Context, req *api.PayRequest) (*api.PayResponse, error) {
	return g.payment.Submit(ctx, req)
}
`,
	})
	calls := map[string]*domain.Fact{}
	for _, f := range facts {
		if f.Kind != domain.FactGrpcCall {
			continue
		}
		if string(f.SourceID) == "symbol:go:example.com/mtest/gateway:(Gateway).Submit" {
			calls["gateway.Submit"] = f
		}
		if string(f.SourceID) == "symbol:go:example.com/mtest/gateway:(Gateway).Pay" {
			calls["gateway.Pay"] = f
		}
	}
	order, ok := calls["gateway.Submit"]
	if !ok {
		t.Fatalf("DI 客户端字段调用未识别 grpc_call: %+v", facts)
	}
	if order.Key != "Submit" {
		t.Errorf("RPC 方法身份 = %q; want Submit", order.Key)
	}
	if svc := svcNameFromID(string(order.TargetID)); svc != "OrderService" {
		t.Errorf("Submit 目标服务 = %q; want OrderService（%s）", svc, order.TargetID)
	}
	pay, ok := calls["gateway.Pay"]
	if !ok {
		t.Fatalf("PaymentService 客户端调用未识别: %+v", facts)
	}
	if svc := svcNameFromID(string(pay.TargetID)); svc != "PaymentService" {
		t.Errorf("Pay 目标服务 = %q; want PaymentService（同名方法不得串线）", svc)
	}
}

func svcNameFromID(id string) string {
	for i := len(id) - 1; i >= 0; i-- {
		if id[i] == ':' {
			s := id[i+1:]
			if len(s) > 4 && s[:4] == "svc." {
				return s[4:]
			}
			return s
		}
	}
	return id
}
