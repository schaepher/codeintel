package ast

import "testing"
import "github.com/schaepher/codeintel/internal/domain"

// TestGrpcRegisterBySignature：R30——手写注册函数（非 .pb.go 文件、
// 函数名无 Register 前缀）经签名识别（grpc.ServiceRegistrar 参数 +
// RegisterService 调用）→ grpc_service 节点（服务名从参数 2 类型
// 提取）+ grpc_impl 边 + registers_service 属性（查询层数据源）。
func TestGrpcRegisterBySignature(t *testing.T) {
	nodes, facts := indexFixture(t, map[string]string{
		"go.mod": "module example.com/mtest\n\ngo 1.21\n",
		"grpc/grpc.go": `package grpc

type ServiceRegistrar interface{ RegisterService(desc any, impl any) }
`,
		"svc/hand.go": `package svc

import "example.com/mtest/grpc"

type QueryServiceServer interface{ Query() string }

type queryServiceImpl struct{}

func (q *queryServiceImpl) Query() string { return "" }

// 手写注册：setupAll 无 Register 前缀、文件非 .pb.go——签名识别
func setupAll(s grpc.ServiceRegistrar, srv QueryServiceServer) {
	s.RegisterService(nil, srv)
}

// 调用点：注册调用发生处（markServiceEntry 在此发射 grpc_service 节点）
func main() {
	var s grpc.ServiceRegistrar
	setupAll(s, &queryServiceImpl{})
}
`,
	})

	svcNode := false
	regAttr := false
	for _, n := range nodes {
		if n.Kind == domain.KindGrpcService && n.Property("service_name") == "QueryService" {
			svcNode = true
		}
		if n.Name == "setupAll" && n.Property("registers_service") == "QueryService" {
			regAttr = true
		}
	}
	if !svcNode {
		t.Error("手写注册函数未发射 grpc_service 节点（签名识别失败）")
	}
	if !regAttr {
		t.Error("注册函数节点缺 registers_service 属性（查询层数据源）")
	}

	implEdge := false
	for _, f := range facts {
		if f.Kind == domain.FactGrpcImpl &&
			string(f.SourceID) == "symbol:go:example.com/mtest/svc:queryServiceImpl" &&
			string(f.TargetID) == "symbol:go:example.com/mtest/svc:svc.QueryService" {
			implEdge = true
		}
	}
	if !implEdge {
		t.Error("grpc_impl 边缺失（queryServiceImpl → svc.QueryService）")
	}
}

// TestGrpcNonRegisterNotMarked：非注册函数（普通 grpc 包使用）不被
// 标记为服务端入口。
func TestGrpcNonRegisterNotMarked(t *testing.T) {
	nodes, _ := indexFixture(t, map[string]string{
		"go.mod": "module example.com/mtest\n\ngo 1.21\n",
		"grpc/grpc.go": `package grpc

type Conn struct{}

func (c *Conn) Invoke(ctx any, method string, args ...any) {}
`,
		"svc/c.go": `package svc

import "example.com/mtest/grpc"

func call(conn *grpc.Conn) {
	conn.Invoke(nil, "/x/y", nil)
}
`,
	})
	for _, n := range nodes {
		if n.Property("registers_service") != "" {
			t.Errorf("普通函数不应带 registers_service: %s", n.Name)
		}
	}
}
