package ast

// T09：micro 注册实参为 `Get().(Handler)` 时，从工厂函数的**全部第一返回槽**
// 证明具体注册实现；存在 nil/接口/不一致返回 → 不产生唯一注册证明。

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

func t09Files(app string) map[string]string {
	return map[string]string{
		"go.mod":           "module example.com/microflow\n\ngo 1.21\n\nrequire go-micro.dev/v2 v2.0.0\n\nreplace go-micro.dev/v2 => ./microstub\n",
		"microstub/go.mod": "module go-micro.dev/v2\n\ngo 1.21\n",
		"microstub/client/client.go": `package client

import "context"

type CallOption interface{ Apply() }
type Request interface{}

type Client interface {
	NewRequest(service, endpoint string, request interface{}) Request
	Call(ctx context.Context, req Request, rsp interface{}, opts ...CallOption) error
}
`,
		"microstub/server/server.go": `package server

type HandlerOption interface{ Apply() }
type Handler interface{ Name() string }

type Server interface {
	Handle(h Handler) error
	NewHandler(h interface{}, opts ...HandlerOption) Handler
}
`,
		"microstub/micro.go": `package micro

import "go-micro.dev/v2/server"

type Option interface{ Name() string }

type Service interface {
	Server() server.Server
	Client() any
}

func NewService(opts ...Option) Service { return nil }
func Name(n string) Option             { return nil }
func RegisterHandler(s server.Server, hdlr interface{}, opts ...server.HandlerOption) error {
	return nil
}
`,
		"pb/proto.go": `package pb

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

type AccountServiceHandler interface {
	Apply(ctx context.Context, in *ApplyRequest, out *ApplyResponse) error
}

func RegisterAccountServiceHandler(s server.Server, hdlr AccountServiceHandler, opts ...server.HandlerOption) error {
	return s.Handle(s.NewHandler(hdlr, opts...))
}
`,
		"app.go": app,
	}
}

func t09Index(t *testing.T, app string) ([]*domain.CodeEntity, []*domain.Fact) {
	t.Helper()
	dir := t.TempDir()
	for path, content := range t09Files(app) {
		writeFile(t, filepath.Join(dir, path), content)
	}
	var nodes []*domain.CodeEntity
	var facts []*domain.Fact
	adapter := &Adapter{}
	repo := &domain.Repository{Path: dir, Module: "example.com/microflow",
		Modules: []string{"example.com/microflow"}}
	pkgs, err := astLoadTestPackages(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := adapter.Index(context.Background(), repo, pkgs, func(item domain.Item) error {
		if item.Node != nil {
			nodes = append(nodes, item.Node)
		}
		if item.Fact != nil {
			facts = append(facts, item.Fact)
		}
		return nil
	}); err != nil {
		s := err.Error()
		if strings.Contains(s, "go: downloading") || strings.Contains(s, "missing go.sum") {
			t.Skip("fixture 依赖需要网络: " + s)
		}
		t.Fatalf("Index: %v", err)
	}
	return nodes, facts
}

const t09AppGood = `package app

import (
	"context"

	"example.com/microflow/pb"
	micro "go-micro.dev/v2"
)

type AccountServer struct{}

func (h *AccountServer) Apply(ctx context.Context, in *pb.ApplyRequest, out *pb.ApplyResponse) error {
	return nil
}

type Container struct{}

func (c *Container) Get() interface{} { return new(AccountServer) }

func Setup() {
	svc := micro.NewService(micro.Name("example.flow.AccountService"))
	c := &Container{}
	pb.RegisterAccountServiceHandler(svc.Server(), c.Get().(pb.AccountServiceHandler))
}
`

const t09AppBad = `package app

import (
	"context"

	"example.com/microflow/pb"
	micro "go-micro.dev/v2"
)

type AccountServer struct{}

func (h *AccountServer) Apply(ctx context.Context, in *pb.ApplyRequest, out *pb.ApplyResponse) error {
	return nil
}

type Container struct{}

var opaque bool

func (c *Container) Get() interface{} {
	if opaque {
		return nil
	}
	return new(AccountServer)
}

func Setup() {
	svc := micro.NewService(micro.Name("example.flow.AccountService"))
	c := &Container{}
	pb.RegisterAccountServiceHandler(svc.Server(), c.Get().(pb.AccountServiceHandler))
}
`

// t09ImplTargets 服务节点的 grpc_impl 目标（实现类型）。
func t09ImplTargets(facts []*domain.Fact, svcSuffix string) []string {
	var out []string
	for _, f := range facts {
		if f.Kind == domain.FactGrpcImpl && strings.HasSuffix(string(f.TargetID), svcSuffix) {
			out = append(out, string(f.SourceID))
		}
	}
	return out
}

func TestT09MicroRegistrationFactoryReturn(t *testing.T) {
	nodes, facts := t09Index(t, t09AppGood)
	// 服务节点存在（AccountService）
	found := false
	for _, n := range nodes {
		if n.Kind == domain.KindGrpcService && n.Property("service_name") == "AccountService" {
			found = true
		}
	}
	if !found {
		t.Fatalf("缺 AccountService 服务节点")
	}
	impls := t09ImplTargets(facts, "svc.AccountService")
	if len(impls) != 1 || !strings.HasSuffix(impls[0], ":AccountServer") {
		t.Fatalf("注册实参工厂第一返回槽应证明 AccountServer，got %v", impls)
	}
}

func TestT09MicroRegistrationFactoryReturnAmbiguous(t *testing.T) {
	_, facts := t09Index(t, t09AppBad)
	if impls := t09ImplTargets(facts, "svc.AccountService"); len(impls) != 0 {
		t.Fatalf("含 nil 返回的工厂不得产生唯一注册证明: %v", impls)
	}
}
