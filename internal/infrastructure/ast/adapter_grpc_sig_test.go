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

// TestGrpcServiceInterfaceByMethod：R30-2/R48——接口方法签名识别——
// 手写服务接口（命名非 Server 结尾、无注册点/注册函数）经方法模式
// （末返回 error + 首参 context.Context + **业务类型定义在 .pb.go**）
// 识别 → grpc_service 节点 + methods 属性。R48（用户要求）：仅 ctx+err
// 不算 grpc——业务类型必须是 pb 类型。
func TestGrpcServiceInterfaceByMethod(t *testing.T) {
	nodes, _ := indexFixture(t, map[string]string{
		"go.mod": "module example.com/mtest\n\ngo 1.21\n",
		// protoc 生成形态：.pb.go 定义请求/响应类型
		"svc/api_grpc.pb.go": `package svc

type DoRequest struct{}
type DoResponse struct{}
type GetRequest struct{}
type GetResponse struct{}
`,
		"svc/api.go": `package svc

import "context"

// 手写服务接口：命名非 Server 结尾、无注册函数——方法模式识别
type HandService interface {
	Do(ctx context.Context, req *DoRequest) (*DoResponse, error)
	Get(ctx context.Context, req *GetRequest) (*GetResponse, error)
}

// 非 grpc（R48 排除）：ctx+err 形态但业务类型非 pb（string/int）——
// 普通业务接口
type PlainService interface {
	Save(ctx context.Context, name string) (int, error)
}

// 非服务接口：方法不符合 grpc 模式（无 ctx 参数）——不得识别
type Repo interface {
	Save(order string) error
}

// 客户端接口（R37 误伤修复）：方法签名同服务模式，但命名 Client 结尾
// ——调用方桩，不得识别为服务（go2o 实测 31 个 XxxServiceClient 误伤）
type HandServiceClient interface {
	Do(ctx context.Context, req *DoRequest) (*DoResponse, error)
	Get(ctx context.Context, req *GetRequest) (*GetResponse, error)
}
`,
	})
	svcNode := false
	methods := ""
	paramTypes := ""
	for _, n := range nodes {
		if n.Kind == domain.KindGrpcService && n.Property("service_name") == "HandService" {
			svcNode = true
			methods = n.Property("methods")
			paramTypes = n.Property("param_types")
		}
		if n.Kind == domain.KindGrpcService && n.Property("service_name") == "PlainService" {
			t.Error("ctx+err 形态但无 pb 类型不应识别为服务（R48——用户要求）")
		}
		if n.Kind == domain.KindGrpcService && n.Property("service_name") == "Repo" {
			t.Error("非 grpc 模式接口（无 ctx 参数）不应识别为服务")
		}
		if n.Kind == domain.KindGrpcService && n.Property("service_name") == "HandServiceClient" {
			t.Error("客户端接口（XxxClient）不应识别为服务（R37 误伤修复）")
		}
	}
	if !svcNode {
		t.Fatal("手写服务接口（方法模式 + pb 类型）未识别为 grpc 服务")
	}
	if methods != "Do,Get" {
		t.Errorf("methods = %q; want Do,Get", methods)
	}
	// R45：param_types——请求对象类型完整路径（首参是 ctx 取第 2 参：
	// *DoRequest/*GetRequest 解指针）
	if paramTypes != "example.com/mtest/svc.DoRequest,example.com/mtest/svc.GetRequest" {
		t.Errorf("param_types = %q; want DoRequest,GetRequest（pb 请求对象类型）", paramTypes)
	}
}

// TestGrpcCallReqType：R45——grpc_call 边 metadata 带请求实参类型
// （req_type）——外部接口判定第二条件（实参 ∉ 本项目服务参数集合）。
func TestGrpcCallReqType(t *testing.T) {
	nodes, facts := indexFixture(t, map[string]string{
		"go.mod": "module example.com/mtest\n\ngo 1.21\n",
		"grpc/grpc.go": `package grpc

type ServiceRegistrar interface{ RegisterService(desc any, impl any) }

type Conn struct{}

func Dial(addr string) (*Conn, error) { return nil, nil }

func (c *Conn) Invoke(ctx any, method string, args, reply any) error { return nil }
`,
		"svc/call.go": `package svc

import (
	"context"
	"example.com/mtest/grpc"
)

type ChargeRequest struct{ Amount int }

// 手写客户端调用（Invoke——请求实参 Args[2]）
func doCharge(conn *grpc.Conn) {
	conn.Invoke(context.Background(), "/ext.pay.PayService/Charge", &ChargeRequest{}, nil)
}
`,
	})
	_ = nodes
	reqType := ""
	methodPath := ""
	for _, f := range facts {
		if f.Kind == domain.FactGrpcCall {
			reqType, _ = f.Metadata["req_type"].(string)
			methodPath, _ = f.Metadata["method_path"].(string)
		}
	}
	if methodPath != "/ext.pay.PayService/Charge" {
		t.Fatalf("method_path = %q; want /ext.pay.PayService/Charge", methodPath)
	}
	if reqType != "example.com/mtest/svc.ChargeRequest" {
		t.Errorf("req_type = %q; want example.com/mtest/svc.ChargeRequest（typePath 解指针）", reqType)
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
