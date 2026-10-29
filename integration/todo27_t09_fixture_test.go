package integration

// todo27 T09 夹具：go-micro 本地 stub + 生成桩 + 服务端（真实 init 用）。

const t09FileGoMod = `module example.com/microflow

go 1.21

require go-micro.dev/v2 v2.0.0

replace go-micro.dev/v2 => ./microstub
`
const t09FileMicrostubGoMod = `module go-micro.dev/v2

go 1.21
`
const t09FileMicrostubClientClientGo = `package client

import "context"

type CallOption interface{ Apply() }

type Request interface{}

type Client interface {
	NewRequest(service, endpoint string, request interface{}) Request
	Call(ctx context.Context, req Request, rsp interface{}, opts ...CallOption) error
}
`
const t09FileMicrostubServerServerGo = `package server

type HandlerOption interface{ Apply() }

type Handler interface{ Name() string }

type Server interface {
	Handle(h Handler) error
	NewHandler(h interface{}, opts ...HandlerOption) Handler
}
`
const t09FileMicrostubMicroGo = `package micro

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
const t09FilePbProtoGo = `package pb

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

type GhostServiceClient interface {
	Apply(ctx context.Context, in *ApplyRequest, opts ...client.CallOption) (*ApplyResponse, error)
}

type ghostServiceClient struct{ c client.Client }

func NewGhostServiceClient(c client.Client) GhostServiceClient {
	return &ghostServiceClient{c: c}
}

func (c *ghostServiceClient) Apply(ctx context.Context, in *ApplyRequest, opts ...client.CallOption) (*ApplyResponse, error) {
	req := c.c.NewRequest("example.flow.GhostService", "GhostService.Apply", in)
	out := &ApplyResponse{}
	if err := c.c.Call(ctx, req, out, opts...); err != nil {
		return nil, err
	}
	return out, nil
}

type GhostServiceHandler interface {
	Apply(ctx context.Context, in *ApplyRequest, out *ApplyResponse) error
}

func RegisterGhostServiceHandler(s server.Server, hdlr GhostServiceHandler, opts ...server.HandlerOption) error {
	return s.Handle(s.NewHandler(hdlr, opts...))
}
`
const t09FileAppGo = `package app

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

func SaveAudit() {}

func SaveOtherAudit() {}

type Container struct{}

func (c *Container) Get() interface{} { return new(AccountServer) }

func Setup() {
	svc := micro.NewService(micro.Name("example.flow.AccountService"))
	c := &Container{}
	pb.RegisterAccountServiceHandler(svc.Server(), c.Get().(pb.AccountServiceHandler))
}

func Entry(ctx context.Context, c client.Client) error {
	cli := pb.NewAccountServiceClient(c)
	_, err := cli.Apply(ctx, &pb.ApplyRequest{})
	return err
}

func SaveOtherAuditX() {}

type OtherServer struct{}

func (h *OtherServer) Apply(ctx context.Context, in *pb.ApplyRequest, out *pb.ApplyResponse) error {
	SaveOtherAudit()
	return nil
}

type OtherContainer struct{}

func (c *OtherContainer) Get() interface{} { return new(OtherServer) }

func SetupOther() {
	svc := micro.NewService(micro.Name("example.flow.OtherService"))
	c := &OtherContainer{}
	pb.RegisterOtherServiceHandler(svc.Server(), c.Get().(pb.OtherServiceHandler))
}

func EntryOther(ctx context.Context, c client.Client) error {
	cli := pb.NewOtherServiceClient(c)
	_, err := cli.Apply(ctx, &pb.ApplyRequest{})
	return err
}

func EntryRepeat(ctx context.Context, c client.Client) error {
	cli := pb.NewAccountServiceClient(c)
	if _, err := cli.Apply(ctx, &pb.ApplyRequest{}); err != nil {
		return err
	}
	_, err := cli.Apply(ctx, &pb.ApplyRequest{})
	return err
}

type GhostServer struct{}

func (h *GhostServer) Apply(ctx context.Context, in *pb.ApplyRequest, out *pb.ApplyResponse) error {
	return nil
}

func EntryGhost(ctx context.Context, c client.Client) error {
	cli := pb.NewGhostServiceClient(c)
	_, err := cli.Apply(ctx, &pb.ApplyRequest{})
	return err
}
`
