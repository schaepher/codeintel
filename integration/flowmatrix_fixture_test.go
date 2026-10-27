//go:build integration

package integration

// todo22 §9.1/§9.2 公开夹具（沿用第 8 节 `example.com/flow` 的结构）：
// 主模块 `example.com/flowmatrix` + 本地 replace 的 `example.com/tx` 与
// go-micro stub（`go-micro.dev/v2`）。在 §8 的装饰器/事务回调/存储/场景检查
// 之上补：trace 提交与批准、准确 RPC 注册（go-micro，§4.1）、DI/AOP 字段注入、
// 非空装饰器、存储 API（按操作用不同方法）、§9.2 的审核完成规则。
//
// 注意：夹具不执行数据库请求（只做源码索引与静态调用验证）；不运行带 nil DB
// 的入口。

const flowMatrixGoMod = `module example.com/flowmatrix

go 1.21

require (
	example.com/tx v0.0.0
	go-micro.dev/v2 v2.0.0
)

replace example.com/tx => ./tx

replace go-micro.dev/v2 => ./microstub
`

const flowMatrixTxGoMod = `module example.com/tx

go 1.21
`

const flowMatrixTxGo = `package tx

import "context"

func Run(ctx context.Context, f func(context.Context) error) error { return f(ctx) }
`

const flowMatrixPb = `package pb

import (
	"context"

	"go-micro.dev/v2/client"
	"go-micro.dev/v2/server"
)

type ApplyRequest struct{ ID int64 }
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

type AccountServiceHandler interface {
	Apply(ctx context.Context, in *ApplyRequest, out *ApplyResponse) error
}

func RegisterAccountServiceHandler(s server.Server, hdlr AccountServiceHandler, opts ...server.HandlerOption) error {
	return s.Handle(s.NewHandler(hdlr, opts...))
}
`
