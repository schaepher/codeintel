package ast

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestGrpcRegisterIfaceNoImpl：接口形态无本地实现 → 回退接口节点
// （查询端 implements 追链兜底）。
func TestGrpcRegisterIfaceNoImpl(t *testing.T) {
	_, facts := indexFixture(t, map[string]string{
		"go.mod":          "module example.com/mtest\n\ngo 1.21\n\nrequire example.com/proto v0.0.0\n\nreplace example.com/proto => ../proto\n",
		"../proto/go.mod": "module example.com/proto\n\ngo 1.21\n",
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

// 无本地实现——接口形态回退
func register(s proto.Registrar) {
	proto.RegisterGreeterServer(s, (proto.GreeterServer)(nil))
}
`,
	})
	found := false
	for _, f := range facts {
		if f.Kind == domain.FactGrpcImpl {
			if string(f.SourceID) != "symbol:go:example.com/proto:GreeterServer" {
				t.Errorf("grpc_impl source = %s; want 接口回退 GreeterServer", f.SourceID)
			}
			found = true
		}
	}
	if !found {
		t.Error("无实现时接口回退应产生 grpc_impl 边（接口 → svc）")
	}
}

// TestGrpcRegisterCtorReturnImpl：注册第二参是构造器调用——函数声明
// 返回接口、函数体 return 具体实现 → grpc_impl 边直指具体实现
// （画图以具体实现展开，而非接口）。
func TestGrpcRegisterCtorReturnImpl(t *testing.T) {
	_, facts := indexFixture(t, map[string]string{
		"go.mod":          "module example.com/mtest\n\ngo 1.21\n\nrequire example.com/proto v0.0.0\n\nreplace example.com/proto => ../proto\n",
		"../proto/go.mod": "module example.com/proto\n\ngo 1.21\n",
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

// 构造器：声明返回接口，函数体 return 具体实现
func newGreeter() proto.GreeterServer {
	return &greeterImpl{}
}

func register(s proto.Registrar) {
	proto.RegisterGreeterServer(s, newGreeter())
}
`,
	})
	found := false
	for _, f := range facts {
		if f.Kind == domain.FactGrpcImpl {
			if string(f.SourceID) != "symbol:go:example.com/mtest:greeterImpl" {
				t.Errorf("grpc_impl source = %s; want 具体实现 greeterImpl（构造器 return 追踪）", f.SourceID)
			}
			found = true
		}
	}
	if !found {
		t.Error("构造器返回接口的注册未产生 grpc_impl 边")
	}
}
