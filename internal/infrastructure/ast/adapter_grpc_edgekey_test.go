package ast

// todo11-1：grpc_call 的关系身份（edge_key = RPC 方法名）——同一函数调同
// 一服务的两个方法必须产出两条边（否则 edges 唯一约束会合并）。

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestGrpcCallEdgeKeyPerMethod：一个函数依次调 Submit 与 Cancel → 两条
// grpc_call 边，Key 各自为方法名。
func TestGrpcCallEdgeKeyPerMethod(t *testing.T) {
	_, facts := indexFixture(t, map[string]string{
		"go.mod": "module example.com/mtest\n\ngo 1.21\n",
		"pb/order.pb.go": `package pb

type Registrar interface{ RegisterService(desc any, impl any) }

type OrderServer interface{}

func RegisterOrderServer(s Registrar, impl OrderServer) { s.RegisterService(nil, impl) }

type OrderClient interface {
	Submit(string) string
	Cancel(string) string
}

func NewOrderClient(conn any) OrderClient { return nil }
`,
		"cli/client.go": `package cli

import "example.com/mtest/pb"

func Run(conn any) {
	c := pb.NewOrderClient(conn)
	c.Submit("a")
	c.Cancel("b")
}
`,
	})

	keys := map[string]bool{}
	var n int
	for _, f := range facts {
		if f.Kind != domain.FactGrpcCall || string(f.SourceID) != "symbol:go:example.com/mtest/cli:Run" {
			continue
		}
		n++
		keys[f.Key] = true
	}
	if n != 2 {
		t.Fatalf("同函数两个 RPC 方法应产出 2 条 grpc_call 边，实为 %d（Key 缺失会被合并）", n)
	}
	if !keys["Submit"] || !keys["Cancel"] {
		t.Errorf("edge_key 应分别是方法名，实为 %v", keys)
	}
}
