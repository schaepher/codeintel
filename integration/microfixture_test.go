//go:build integration

package integration

// todo22 §4.1 的公开夹具：go-micro 服务协议（客户端生成桩 + 服务端注册）。
//
// 真实源码形态（不联网）：
//   - go-micro.dev/v2 用本地 stub 模块 + replace 提供（client.Client /
//     server.Server / micro.NewService/micro.Name）；
//   - pb 包按生成代码形态给出客户端桩（NewRequest + Call）与
//     Register<X>Handler（server.Server + XHandler 接口）；
//   - app 包注册两个服务（AccountService / OtherService），两者都有 Apply。
//
// 与 todo22 §4.1 的期望一致：客户端只请求 example.flow.AccountService.Apply，
// 输出只能续到 AccountServer.Apply；同名方法属于其他服务时不得串线。

import (
	"os"
	"path/filepath"
	"testing"
)

const microFixtureGoMod = `module example.com/microflow

go 1.21

require go-micro.dev/v2 v2.0.0

replace go-micro.dev/v2 => ./microstub
`

const microFixtureStubGoMod = `module go-micro.dev/v2

go 1.21
`

const microFixtureStubClient = `package client

import "context"

type CallOption interface{ Apply() }

type Request interface{}

type Client interface {
	NewRequest(service, endpoint string, request interface{}) Request
	Call(ctx context.Context, req Request, rsp interface{}, opts ...CallOption) error
}
`

const microFixtureStubServer = `package server

type HandlerOption interface{ Apply() }

type Handler interface{ Name() string }

type Server interface {
	Handle(h Handler) error
	NewHandler(h interface{}, opts ...HandlerOption) Handler
}
`

const microFixtureStubRoot = `package micro

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

// microFixtureApp 业务/服务端源码（注册两服务 + 两个入口）。
const microFixtureApp = `package app

import (
	"context"

	"example.com/microflow/pb"
	micro "go-micro.dev/v2"
	"go-micro.dev/v2/client"
)

type AccountServer struct{}

func (h *AccountServer) Apply(ctx context.Context, in *pb.ApplyRequest, out *pb.ApplyResponse) error {
	SaveAudit()
	return nil
}

type OtherServer struct{}

func (h *OtherServer) Apply(ctx context.Context, in *pb.ApplyRequest, out *pb.ApplyResponse) error {
	SaveOtherAudit()
	return nil
}

func SaveAudit() {}

func SaveOtherAudit() {}

func Setup() {
	svc := micro.NewService(micro.Name("example.flow.AccountService"))
	pb.RegisterAccountServiceHandler(svc.Server(), new(AccountServer))
	other := micro.NewService(micro.Name("example.flow.OtherService"))
	pb.RegisterOtherServiceHandler(other.Server(), new(OtherServer))
}

func Entry(ctx context.Context, c client.Client) error {
	cli := pb.NewAccountServiceClient(c)
	_, err := cli.Apply(ctx, &pb.ApplyRequest{})
	return err
}

func EntryOther(ctx context.Context, c client.Client) error {
	cli := pb.NewOtherServiceClient(c)
	_, err := cli.Apply(ctx, &pb.ApplyRequest{})
	return err
}
`

// microFixture 夹具路径（按变体覆盖任意源文件后 init）。
type microFixture struct {
	dir  string
	path string // app.go 路径
}

// writeMicroFixture 写夹具到临时目录（overrides 覆盖同名相对路径文件内容）。
func writeMicroFixture(t *testing.T, overrides map[string]string) microFixture {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod":                     microFixtureGoMod,
		"microstub/go.mod":           microFixtureStubGoMod,
		"microstub/client/client.go": microFixtureStubClient,
		"microstub/server/server.go": microFixtureStubServer,
		"microstub/micro.go":         microFixtureStubRoot,
		"pb/proto.go":                microFixturePb,
		"app.go":                     microFixtureApp,
	}
	for k, v := range overrides {
		files[k] = v
	}
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, p, body)
	}
	return microFixture{dir: root, path: filepath.Join(root, "app.go")}
}
