package ast

// todo22 §4.1：go-micro 服务协议识别的单元覆盖（真实 go/packages 类型信息 +
// 本地 stub 模块 replace，不联网）。
//
// 断言要点：
//   - 生成客户端桩的 NewRequest+Call → grpc_call 边（source=桩方法）
//   - 端点字符串/服务完整身份分别记录（非标准服务名不被过滤）
//   - 生成注册函数（server.Server + XxxHandler 接口）→ grpc_service 节点
//     + grpc_impl 边；完整身份来自 micro.Name 的局部变量绑定
//   - 同名方法属于不同服务不串线（AccountService.Apply vs OtherService.Apply）

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

const microStubGoMod = `module go-micro.dev/v2

go 1.21
`

const microStubClient = `package client

import "context"

type CallOption interface{ Apply() }

type Request interface{}

type Client interface {
	NewRequest(service, endpoint string, request interface{}) Request
	Call(ctx context.Context, req Request, rsp interface{}, opts ...CallOption) error
}
`

const microStubServer = `package server

type HandlerOption interface{ Apply() }

type Handler interface{ Name() string }

type Server interface {
	Handle(h Handler) error
	NewHandler(h interface{}, opts ...HandlerOption) Handler
}
`

const microStubRoot = `package micro

import "go-micro.dev/v2/server"

type Option interface{ Name() string }

type Service interface {
	Server() server.Server
	Client() any
}

func NewService(opts ...Option) Service { return nil }

func Name(n string) Option { return nil }

func RegisterHandler(s server.Server, hdlr interface{}, opts ...server.HandlerOption) error {
	return nil
}
`

const microFixturePb = `package pb

import (
	"context"

	"go-micro.dev/v2/client"
	"go-micro.dev/v2/server"
)

type ApplyRequest struct{ ID string }
type ApplyResponse struct{ OK bool }

type AccountServiceClient interface {
	Apply(ctx context.Context, in *ApplyRequest, opts ...client.CallOption) (*ApplyResponse, error)
}

type accountServiceClient struct{ c client.Client }

func NewAccountServiceClient(c client.Client) AccountServiceClient {
	return &accountServiceClient{c: c}
}

func (c *accountServiceClient) Apply(ctx context.Context, in *ApplyRequest, opts ...client.CallOption) (*ApplyResponse, error) {
	req := c.c.NewRequest("example.flow.AccountService", "AccountService.Apply", in)
	out := &ApplyResponse{}
	if err := c.c.Call(ctx, req, out, opts...); err != nil {
		return nil, err
	}
	return out, nil
}

type OtherServiceClient interface {
	Apply(ctx context.Context, in *ApplyRequest, opts ...client.CallOption) (*ApplyResponse, error)
}

type otherServiceClient struct{ c client.Client }

func NewOtherServiceClient(c client.Client) OtherServiceClient {
	return &otherServiceClient{c: c}
}

func (c *otherServiceClient) Apply(ctx context.Context, in *ApplyRequest, opts ...client.CallOption) (*ApplyResponse, error) {
	req := c.c.NewRequest("example.flow.OtherService", "OtherService.Apply", in)
	out := &ApplyResponse{}
	if err := c.c.Call(ctx, req, out, opts...); err != nil {
		return nil, err
	}
	return out, nil
}

type AccountServiceHandler interface {
	Apply(ctx context.Context, in *ApplyRequest, out *ApplyResponse) error
}

func RegisterAccountServiceHandler(s server.Server, hdlr AccountServiceHandler, opts ...server.HandlerOption) error {
	return s.Handle(s.NewHandler(hdlr, opts...))
}

type OtherServiceHandler interface {
	Apply(ctx context.Context, in *ApplyRequest, out *ApplyResponse) error
}

func RegisterOtherServiceHandler(s server.Server, hdlr OtherServiceHandler, opts ...server.HandlerOption) error {
	return s.Handle(s.NewHandler(hdlr, opts...))
}
`

const microFixtureApp = `package app

import (
	"context"

	"example.com/mtest/pb"
	micro "go-micro.dev/v2"
	"go-micro.dev/v2/client"
)

type AccountServer struct{}

func (h *AccountServer) Apply(ctx context.Context, in *pb.ApplyRequest, out *pb.ApplyResponse) error {
	return nil
}

type OtherServer struct{}

func (h *OtherServer) Apply(ctx context.Context, in *pb.ApplyRequest, out *pb.ApplyResponse) error {
	return nil
}

func Setup() {
	svc := micro.NewService(micro.Name("example.flow.AccountService"))
	pb.RegisterAccountServiceHandler(svc.Server(), new(AccountServer))
	other := micro.NewService(micro.Name("example.flow.OtherService"))
	pb.RegisterOtherServiceHandler(other.Server(), new(OtherServer))
}

func Caller(ctx context.Context, c client.Client) error {
	cli := pb.NewAccountServiceClient(c)
	_, err := cli.Apply(ctx, &pb.ApplyRequest{})
	return err
}
`

// microFixtureFiles 组装修订后的模块（stub 模块用 replace 指向本地目录）。
func microFixtureFiles() map[string]string {
	return map[string]string{
		"go.mod": `module example.com/mtest

go 1.21

require go-micro.dev/v2 v2.0.0

replace go-micro.dev/v2 => ./microstub
`,
		"microstub/go.mod":           microStubGoMod,
		"microstub/client/client.go": microStubClient,
		"microstub/server/server.go": microStubServer,
		"microstub/micro.go":         microStubRoot,
		"pb/proto.go":                microFixturePb,
		"app/app.go":                 microFixtureApp,
	}
}

// mergedNode 按节点 ID 合并属性（模拟库内 UPSERT——同一服务节点由客户端桩
// 与服务端注册两处发射，真实图里 properties 是 json_patch 合并后的视图）。
func mergedNode(t *testing.T, nodes []*domain.CodeEntity, id string) *domain.CodeEntity {
	t.Helper()
	out := &domain.CodeEntity{ID: domain.CanonicalID(id), Properties: map[string]any{}}
	found := false
	for _, n := range nodes {
		if string(n.ID) != id {
			continue
		}
		found = true
		out.Kind = n.Kind
		for k, v := range n.Properties {
			out.Properties[k] = v
		}
	}
	if !found {
		t.Fatalf("node not found: %s", id)
	}
	return out
}

func TestIndexMicroServiceRegistrationAndStub(t *testing.T) {
	nodes, facts := indexFixture(t, microFixtureFiles())

	acct := mergedNode(t, nodes, "symbol:go:example.com/mtest/pb:svc.AccountService")
	if acct.Kind != domain.KindGrpcService {
		t.Errorf("AccountService kind = %s; want grpc_service", acct.Kind)
	}
	if got := acct.Property("rpc_protocol"); got != domain.RPCProtocolMicro {
		t.Errorf("rpc_protocol = %q; want micro", got)
	}
	if got := acct.Property("rpc_service"); got != "example.flow.AccountService" {
		t.Errorf("rpc_service = %q; want example.flow.AccountService（完整身份来自 micro.Name 绑定）", got)
	}
	if got := acct.Property("service_name"); got != "AccountService" {
		t.Errorf("service_name = %q; want AccountService（与既有 grpc-routes 口径一致）", got)
	}
	if got := acct.Property("methods"); got != "Apply" {
		t.Errorf("methods = %q; want Apply", got)
	}

	other := mergedNode(t, nodes, "symbol:go:example.com/mtest/pb:svc.OtherService")
	if got := other.Property("rpc_service"); got != "example.flow.OtherService" {
		t.Errorf("OtherService rpc_service = %q", got)
	}

	// grpc_impl：实现类型 → 服务（同方法名不同服务不串线）
	findFact(t, facts, "symbol:go:example.com/mtest/app:AccountServer",
		"symbol:go:example.com/mtest/pb:svc.AccountService", string(domain.FactGrpcImpl))
	findFact(t, facts, "symbol:go:example.com/mtest/app:OtherServer",
		"symbol:go:example.com/mtest/pb:svc.OtherService", string(domain.FactGrpcImpl))

	// grpc_call：客户端桩方法 → 服务（edge_key = RPC 方法名）
	acctCall := findFact(t, facts, "symbol:go:example.com/mtest/pb:(accountServiceClient).Apply",
		"symbol:go:example.com/mtest/pb:svc.AccountService", string(domain.FactGrpcCall))
	if acctCall.Key != "Apply" {
		t.Errorf("grpc_call key = %q; want Apply", acctCall.Key)
	}
	if got, _ := acctCall.Metadata["rpc_service"].(string); got != "example.flow.AccountService" {
		t.Errorf("grpc_call metadata rpc_service = %v", acctCall.Metadata["rpc_service"])
	}
	otherCall := findFact(t, facts, "symbol:go:example.com/mtest/pb:(otherServiceClient).Apply",
		"symbol:go:example.com/mtest/pb:svc.OtherService", string(domain.FactGrpcCall))
	if otherCall.Key != "Apply" {
		t.Errorf("OtherService grpc_call key = %q; want Apply", otherCall.Key)
	}

	// 注册站点：注册函数节点带 registers_service（短名）+ 完整身份
	// （同一 Setup 注册多个服务时调用方单属性会互相覆盖，故按注册函数记录）
	reg := mergedNode(t, nodes, "symbol:go:example.com/mtest/pb:RegisterAccountServiceHandler")
	if got := reg.Property("registers_service"); got != "AccountService" {
		t.Errorf("RegisterAccountServiceHandler registers_service = %q", got)
	}
	if got := reg.Property("registers_rpc_service"); got != "example.flow.AccountService" {
		t.Errorf("RegisterAccountServiceHandler registers_rpc_service = %q", got)
	}
	regOther := mergedNode(t, nodes, "symbol:go:example.com/mtest/pb:RegisterOtherServiceHandler")
	if got := regOther.Property("registers_rpc_service"); got != "example.flow.OtherService" {
		t.Errorf("RegisterOtherServiceHandler registers_rpc_service = %q", got)
	}
}

func TestIndexMicroClientStubRequiresLiteralIdentity(t *testing.T) {
	files := microFixtureFiles()
	files["pb/proto.go"] = strings.Replace(microFixturePb,
		`c.c.NewRequest("example.flow.AccountService", "AccountService.Apply", in)`,
		`c.c.NewRequest(dynamicName(), "AccountService.Apply", in)`, 1)
	files["pb/proto.go"] += `
func dynamicName() string { return "example.flow.AccountService" }
`
	_, facts := indexFixture(t, files)
	var call *domain.Fact
	for _, f := range facts {
		if f.Kind == domain.FactGrpcCall && string(f.SourceID) == "symbol:go:example.com/mtest/pb:(accountServiceClient).Apply" {
			call = f
		}
	}
	if call == nil {
		t.Fatalf("端点字面量仍应保留 RPC 调用（服务身份未知）")
	}
	if v, _ := call.Metadata["rpc_service"].(string); v != "" {
		t.Errorf("动态服务名不得被猜成完整身份: rpc_service=%q", v)
	}
}
