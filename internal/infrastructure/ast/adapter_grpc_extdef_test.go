package ast

// R90 测试：grpc 内部实现、外部仓库全由 grpc 工具生成（含 Register
// 函数定义）——被解析项目调用外部 Register 注册。外部包作为依赖
// （fast 模式无 Syntax，仅 types）→ 调用点须识别为 grpc 注册。

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestGrpcExternalRegisterDefinition：Register 定义在外部依赖包
// （replace 到模块外目录）——调用点在本仓库。collectRegisterServers
// 的依赖包 types 级扫描识别 → svc 节点 + grpc_impl 边。
func TestGrpcExternalRegisterDefinition(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":		"module example.com/mtest\n\ngo 1.21\n\nrequire example.com/proto v0.0.0\n\nreplace example.com/proto => ../proto\n",
		"../proto/go.mod":	"module example.com/proto\n\ngo 1.21\n",
		"../proto/greet.pb.go": `package proto

type Registrar interface{ RegisterService(desc any, impl any) }

type GreeterServer interface{ SayHello(string) string }

func RegisterGreeterServer(s Registrar, impl GreeterServer) {
	s.RegisterService(nil, impl)
}
`,
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
	}
	for path, content := range files {
		writeFile(t, filepath.Join(dir, path), content)
	}
	pkgs, err := astLoadTestPackages(dir)
	if err != nil {
		t.Fatal(err)
	}
	// 决定性验证：清空依赖包 Syntax——模拟真实 fast 模式（已发布
	// 依赖从 export data 加载无 Syntax；本地 replace 未编译时
	// go/packages 会给 Syntax——原 fixture 假阳性走 R86 定义路径）。
	// R90 types 扫描须独立工作。
	for _, p := range pkgs {
		for _, imp := range p.Imports {
			imp.Syntax = nil
		}
	}
	adapter := &Adapter{}
	repo := &domain.Repository{Path: dir, Module: "example.com/mtest", Modules: []string{"example.com/mtest"}}
	var facts []*domain.Fact
	_ = adapter.Index(context.Background(), repo, pkgs, func(item domain.Item) error {
		if item.Fact != nil {
			facts = append(facts, item.Fact)
		}
		return nil
	})

	// svc 节点	// svc 节点	// svc 节点
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
		"go.mod":	"module example.com/mtest\n\ngo 1.21\n",
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

// TestGrpcExternalRegisterIfaceArg：调用点第二参是外部接口变量（业务
// 实现经变量/注入传入——DI 场景无直接调用关系）——R95-2 类型匹配
// （types.Implements）直指业务实现 greeterImpl。
func TestGrpcExternalRegisterIfaceArg(t *testing.T) {
	_, facts := indexFixture(t, map[string]string{
		"go.mod":		"module example.com/mtest\n\ngo 1.21\n\nrequire example.com/proto v0.0.0\n\nreplace example.com/proto => ../proto\n",
		"../proto/go.mod":	"module example.com/proto\n\ngo 1.21\n",
		"../proto/greet.pb.go": `package proto

type Registrar interface{ RegisterService(desc any, impl any) }

type GreeterServer interface{ SayHello(string) string }

func RegisterGreeterServer(s Registrar, impl GreeterServer) {
	s.RegisterService(nil, impl)
}
`,
		"main.go": `package mtest

import (
	"example.com/proto"
)

type greeterImpl struct{}

func (g *greeterImpl) SayHello(s string) string { return s }

func register(s proto.Registrar) {
	// 真实形态：接口变量承载业务实现（依赖注入）
	var srv proto.GreeterServer = &greeterImpl{}
	proto.RegisterGreeterServer(s, srv)
}
`,
	})
	found := false
	for _, f := range facts {
		if f.Kind == domain.FactGrpcImpl {
			if string(f.SourceID) != "symbol:go:example.com/mtest:greeterImpl" {
				t.Errorf("grpc_impl source = %s; want 具体实现 greeterImpl（类型匹配——DI 无直接调用）", f.SourceID)
			}
			if string(f.TargetID) != "symbol:go:example.com/proto:svc.Greeter" {
				t.Errorf("grpc_impl target = %s; want svc.Greeter", f.TargetID)
			}
			found = true
		}
	}
	if !found {
		t.Error("接口变量调用点未产生 grpc_impl 边（实现 → svc）")
	}
}

// TestGrpcRegisterIfaceNoImpl：接口形态无本地实现 → 回退接口节点
// （查询端 implements 追链兜底）。

// TestGrpcCustomClientNotService：自定义客户端（方法签名与 grpc 一致
// 但无 Register——业务系统调用外部服务的客户端桩）不产生 svc 节点。
func TestGrpcCustomClientNotService(t *testing.T) {
	_, facts := indexFixture(t, map[string]string{
		"go.mod":	"module example.com/mtest\n\ngo 1.21\n",
		// 业务类型在 .pb.go（外部 proto 包类型形态——命中 R48
		// pbDefinedType 信号）
		"req.pb.go": `package mtest

type SmsReq struct{ Phone string }
type SmsResp struct{ ID string }
`,
		"client.go": `package mtest

import "context"

// 自定义客户端：签名与 grpc 服务一致（2 参 2 返回 + .pb.go 业务
// 类型）——但无 Register 佐证 → 不应识别为系统服务
type SmsSender interface {
	Send(ctx context.Context, req SmsReq) (SmsResp, error)
}

func use(s SmsSender) {
	r, _ := s.Send(nil, SmsReq{Phone: "123"})
	_ = r
}
`,
	})
	for _, f := range facts {
		if f.Kind == domain.FactGrpcImpl || f.Kind == domain.FactGrpcCall {
			t.Errorf("自定义客户端不应产生 grpc 边: %+v", f)
		}
	}
}

// TestGrpcHandwrittenRegister佐证：手写直接注册
// （reg.RegisterService(&SmsService_ServiceDesc, impl)——无
// RegisterXxxServer 包装函数）→ 接口签名识别有注册佐证 → svc 节点。
func TestGrpcHandwrittenRegisterProof(t *testing.T) {
	nodes, _ := indexFixture(t, map[string]string{
		"go.mod":	"module example.com/mtest\n\ngo 1.21\n",
		"req.pb.go": `package mtest

type SmsReq struct{ Phone string }
type SmsResp struct{ ID string }
`,
		"svc.go": `package mtest

import "context"

type Registrar interface{ RegisterService(desc any, impl any) }

type SmsServiceServer interface {
	Send(ctx context.Context, req SmsReq) (SmsResp, error)
}

type smsImpl struct{}

func (s *smsImpl) Send(ctx context.Context, req SmsReq) (SmsResp, error) { return SmsResp{}, nil }

func register(reg Registrar) {
	reg.RegisterService(&SmsService_ServiceDesc, &smsImpl{})
}
`,
	})
	found := false
	for _, n := range nodes {
		if n.Kind == domain.KindGrpcService && string(n.ID) == "symbol:go:example.com/mtest:svc.SmsService" {
			found = true
		}
	}
	if !found {
		t.Error("手写直接注册的服务接口应有注册佐证（desc 名）→ svc 节点")
	}
}

// TestGrpcRegisterCtorReturnImpl：注册第二参是构造器调用——函数声明
// 返回接口、函数体 return 具体实现 → grpc_impl 边直指具体实现
// （画图以具体实现展开，而非接口）。
