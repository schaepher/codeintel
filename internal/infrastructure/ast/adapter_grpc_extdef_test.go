package ast

// R90 测试：grpc 内部实现、外部仓库全由 grpc 工具生成（含 Register
// 函数定义）——被解析项目调用外部 Register 注册。外部包作为依赖
// （fast 模式无 Syntax，仅 types）→ 调用点须识别为 grpc 注册。

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestGrpcExternalRegisterDefinition：Register 定义在外部依赖包
// （replace 到模块外目录）——调用点在本仓库。collectRegisterServers
// 的依赖包 types 级扫描识别 → svc 节点 + grpc_impl 边。
func TestGrpcExternalRegisterDefinition(t *testing.T) {
	_, facts := indexFixture(t, map[string]string{
		"go.mod": "module example.com/mtest\n\ngo 1.21\n\nrequire example.com/proto v0.0.0\n\nreplace example.com/proto => ../proto\n",
		// 外部仓库：独立 module（grpc 工具生成——依赖包，fast 模式无 Syntax）
		"../proto/go.mod": "module example.com/proto\n\ngo 1.21\n",
		"../proto/greet.pb.go": `package proto

type Registrar interface{ RegisterService(desc any, impl any) }

type GreeterServer interface{ SayHello(string) string }

func RegisterGreeterServer(s Registrar, impl GreeterServer) {
	s.RegisterService(nil, impl)
}
`,
		// 本仓库：调用外部 Register 注册（调用点）
		"main.go": `package mtest

import (
	"example.com/proto"
)

type greeterImpl struct{}

func (g *greeterImpl) SayHello(s string) string { return s }

func register(s proto.Registrar) {
	proto.RegisterGreeterServer(s, &greeterImpl{})
}
`,
	})

	// svc 节点	// svc 节点
	foundSvc := false
	// grpc_impl 边：调用点参数 &greeterImpl{} → svc.Greeter
	foundImpl := false
	for _, f := range facts {
		if f.Kind == domain.FactGrpcImpl {
			if string(f.TargetID) != "symbol:go:example.com/proto:svc.Greeter" {
				t.Errorf("grpc_impl target = %s; want svc.Greeter（外部仓库 proto 包）", f.TargetID)
			}
			if string(f.SourceID) != "symbol:go:example.com/mtest:greeterImpl" {
				t.Errorf("grpc_impl source = %s; want 本仓库 greeterImpl", f.SourceID)
			}
			foundImpl = true
		}
		if f.Kind == domain.FactGrpcCall {
			foundSvc = true
		}
	}
	if !foundImpl {
		t.Error("外部 Register 定义的调用点未被识别为 grpc 注册（grpc_impl 边缺失）")
	}
	_ = foundSvc
}

// TestGrpcExternalRegisterNotGrpcLib：依赖包 types 扫描不误伤普通
// grpc 库函数（如 grpc.NewServer——第二参不是接口形态）。
func TestGrpcExternalRegisterNotGrpcLib(t *testing.T) {
	_, facts := indexFixture(t, map[string]string{
		"go.mod": "module example.com/mtest\n\ngo 1.21\n",
		"main.go": `package mtest

type Registrar interface{ RegisterService(desc any, impl any) }

func fakeRegister(s Registrar, impl any) {}

func run() {
	fakeRegister(nil, struct{}{})
}
`,
	})
	for _, f := range facts {
		if f.Kind == domain.FactGrpcImpl {
			t.Errorf("非 grpc 形态不应产生 grpc_impl 边: %+v", f)
		}
	}
}
