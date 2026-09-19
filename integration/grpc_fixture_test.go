//go:build integration

package integration

// grpc / 值节点 fixture 与查库 helper（Q252b：从 fixture_test.go 拆出——
// 行数治理；主体是"真实形态 grpc fixture + 匿名分配锚点查询"）。

import (
	"path/filepath"
	"testing"

	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// writeGrpcMonoFixture 造**真实形态**的 protoc 生成代码 fixture（svc_a 调
// svc_b）。R30 起 grpc 注册函数按**签名**识别（首参 grpc.ServiceRegistrar /
// *grpc.Server，或函数体调用 RegisterService）——`func RegisterXxxServer(s any,
// impl XxxServer)` 这类简化形态不再识别（Q252b：原 fixture 用它，R29 改动
// 后 module-calls 的 grpc_impl 边缺失、to_module 空，测试长红）。
// google.golang.org/grpc 用本地 stub module + replace（与 fixtureapp 的
// gorm/xorm 同法，不联网）；clientCode 决定客户端形态（生成代码 / 手写
// Invoke）。
func writeGrpcMonoFixture(t *testing.T, dir, clientCode string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "go.mod"), `module example.com/mono

go 1.21

require google.golang.org/grpc v0.0.0

replace google.golang.org/grpc => ./grpcstub
`)
	writeFile(t, filepath.Join(dir, "grpcstub/go.mod"), "module google.golang.org/grpc\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "grpcstub/grpc.go"), `package grpc

import "context"

type ServiceRegistrar interface{ RegisterService(desc *ServiceDesc, ss any) }

type ServiceDesc struct {
	ServiceName string
	HandlerType any
	Methods     []MethodDesc
}

type MethodDesc struct {
	MethodName string
	Handler    any
}

type Server struct{}

func (s *Server) RegisterService(desc *ServiceDesc, ss any) {}

type ClientConn struct{}

func (c *ClientConn) Invoke(ctx context.Context, method string, args ...any) {}

type ClientConnInterface interface {
	Invoke(ctx context.Context, method string, args ...any)
}

type CallOption struct{}
`)
	writeFile(t, filepath.Join(dir, "pb/greet.pb.go"), `package pb

import (
	"context"

	"google.golang.org/grpc"
)

type HelloRequest struct{ Name string }

type HelloReply struct{ Message string }

type GreeterServer interface {
	SayHello(context.Context, *HelloRequest) (*HelloReply, error)
}

func RegisterGreeterServer(s grpc.ServiceRegistrar, srv GreeterServer) {
	s.RegisterService(&greeterServiceDesc, srv)
}

var greeterServiceDesc = grpc.ServiceDesc{
	ServiceName: "pb.Greeter",
	HandlerType: (*GreeterServer)(nil),
}

type GreeterClient interface {
	SayHello(ctx context.Context, in *HelloRequest, opts ...grpc.CallOption) (*HelloReply, error)
}

type greeterClient struct{ cc grpc.ClientConnInterface }

func NewGreeterClient(cc grpc.ClientConnInterface) GreeterClient { return &greeterClient{cc} }

func (c *greeterClient) SayHello(ctx context.Context, in *HelloRequest, opts ...grpc.CallOption) (*HelloReply, error) {
	return &HelloReply{}, nil
}
`)
	writeFile(t, filepath.Join(dir, "svc_a/client.go"), clientCode)
	writeFile(t, filepath.Join(dir, "svc_b/server.go"), `package svc_b

import (
	"context"

	"example.com/mono/pb"
	"google.golang.org/grpc"
)

type greeterImpl struct{}

func (g *greeterImpl) SayHello(ctx context.Context, in *pb.HelloRequest) (*pb.HelloReply, error) {
	return &pb.HelloReply{}, nil
}

func register(s *grpc.Server) {
	pb.RegisterGreeterServer(s, &greeterImpl{})
}
`)
	writeFile(t, filepath.Join(dir, "modules.yaml"), `modules:
  - prefix: "svc_a"
    name: "svc_a"
  - prefix: "svc_b"
    name: "svc_b"
`)
}

// allocValueID 查函数内匿名分配（ssa_op=alloc）的 ssa_value 节点 ID。
// 匿名分配槽位命名（tN → 类型短名，Q235-7/§93）会演进，测试锚点从库查
// 而不是硬编码，避免命名规则改动让测试变红。
func allocValueID(t *testing.T, repo *sqlite.Repo, funcID string) string {
	t.Helper()
	rows, err := repo.Query(`SELECT id FROM nodes
		WHERE kind = 'ssa_value'
		  AND json_extract(properties, '$.func_id') = ?
		  AND lower(json_extract(properties, '$.ssa_op')) = 'alloc'
		ORDER BY id LIMIT 1`, funcID)
	if err != nil {
		t.Fatalf("allocValueID: %v", err)
	}
	defer rows.Close()
	if rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan allocValueID: %v", err)
		}
		return id
	}
	return ""
}
