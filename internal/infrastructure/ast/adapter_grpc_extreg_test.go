package ast

// R86 测试：grpc 注册点在外部仓库（单独编译）——本仓库只有 proto
// 生成代码（RegisterXxxServer 定义 + XxxServer 接口）+ 服务端实现，
// 无 Register 调用点。仍须识别 grpc_service 节点 + grpc_impl 边
// （否则 wiki 流程页 grpc 子页无调用链）。

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestGrpcRegisterNoCallSite：无注册调用点（外部仓库注册）——从
// Register 函数定义（.pb.go 签名）发射 svc 节点 + grpc_impl 边。
func TestGrpcRegisterNoCallSite(t *testing.T) {
	_, facts := indexFixture(t, map[string]string{
		"go.mod": "module example.com/mtest\n\ngo 1.21\n",
		"pb/greet.pb.go": `package pb

type Registrar interface{ RegisterService(desc any, impl any) }

type GreeterServer interface{ SayHello(string) string }

// 注册函数定义（proto 生成代码）——调用点在其他仓库
func RegisterGreeterServer(s Registrar, impl GreeterServer) {
	s.RegisterService(nil, impl)
}
`,
		"impl/greeter.go": `package impl

type greeterImpl struct{}

func (g *greeterImpl) SayHello(s string) string { return s }
`,
	})

	// grpc_impl 边：实现类型 → 服务（从 Register 定义 + types.Implements
	// 扫描得出——不依赖调用点）
	gotImpl := false
	for _, f := range facts {
		if f.Kind == domain.FactGrpcImpl {
			if string(f.TargetID) != "symbol:go:example.com/mtest/pb:svc.Greeter" {
				t.Errorf("grpc_impl target = %s; want svc.Greeter", f.TargetID)
			}
			if string(f.SourceID) != "symbol:go:example.com/mtest/impl:greeterImpl" {
				t.Errorf("grpc_impl source = %s; want greeterImpl", f.SourceID)
			}
			gotImpl = true
		}
	}
	if !gotImpl {
		t.Error("外部注册场景 grpc_impl 边缺失（Register 定义应能定位实现类型）")
	}
}

// TestGrpcRegisterUnimplementedExcluded：Unimplemented 桩不产生
// grpc_impl 边（查询端 grpcImpl 会追 implements——构建期直接排除）。
func TestGrpcRegisterUnimplementedExcluded(t *testing.T) {
	_, facts := indexFixture(t, map[string]string{
		"go.mod": "module example.com/mtest\n\ngo 1.21\n",
		"pb/greet.pb.go": `package pb

type Registrar interface{ RegisterService(desc any, impl any) }

type GreeterServer interface{ SayHello(string) string }

func RegisterGreeterServer(s Registrar, impl GreeterServer) {
	s.RegisterService(nil, impl)
}
`,
		"impl/greeter.go": `package impl

type UnimplementedGreeterServer struct{}

func (g *UnimplementedGreeterServer) SayHello(s string) string { return s }

type greeterImpl struct{}

func (g *greeterImpl) SayHello(s string) string { return s }
`,
	})
	for _, f := range facts {
		if f.Kind == domain.FactGrpcImpl && string(f.SourceID) == "symbol:go:example.com/mtest/impl:UnimplementedGreeterServer" {
			t.Error("Unimplemented 桩不应产生 grpc_impl 边")
		}
	}
}
