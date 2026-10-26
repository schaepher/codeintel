//go:build integration

package integration

// todo13-16 A2：不同 proto 包的同名服务不得串线（真实索引 + 跨仓续接）。
//   - 共享 proto 模块（ci / other 两个包）各声明 RevalidationOrderService
//   - client 调两者；server-a / server-b 分别实现
//   - 交换候选顺序/查询先后：映射不变；Submit/Cancel 不互连

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

const todo13ProtoStub = `package grpcstub

type ServiceDesc struct{ ServiceName string }

type ServiceRegistrar interface {
	RegisterService(desc *ServiceDesc, impl any)
}
`

func todo13ProtoSvc(pkgName, svc, reqPrefix string) string {
	return "package " + pkgName + `

import (
	"context"

	"example.com/proto/grpcstub"
)

type ` + reqPrefix + `Request struct{ ID string }
type ` + reqPrefix + `Response struct{ OK bool }

type RevalidationOrderServiceClient interface {
	Submit(ctx context.Context, in *` + reqPrefix + `Request, opts ...any) (*` + reqPrefix + `Response, error)
	Cancel(ctx context.Context, in *` + reqPrefix + `Request, opts ...any) (*` + reqPrefix + `Response, error)
}

type ` + pkgName + `Client struct{}

func (c *` + pkgName + `Client) Submit(ctx context.Context, in *` + reqPrefix + `Request, opts ...any) (*` + reqPrefix + `Response, error) {
	return &` + reqPrefix + `Response{OK: true}, nil
}

func (c *` + pkgName + `Client) Cancel(ctx context.Context, in *` + reqPrefix + `Request, opts ...any) (*` + reqPrefix + `Response, error) {
	return &` + reqPrefix + `Response{OK: true}, nil
}

func NewRevalidationOrderServiceClient() RevalidationOrderServiceClient { return &` + pkgName + `Client{} }

type RevalidationOrderServiceServer interface {
	Submit(ctx context.Context, in *` + reqPrefix + `Request) (*` + reqPrefix + `Response, error)
	Cancel(ctx context.Context, in *` + reqPrefix + `Request) (*` + reqPrefix + `Response, error)
}

func RegisterRevalidationOrderServiceServer(s grpcstub.ServiceRegistrar, srv RevalidationOrderServiceServer) {
	s.RegisterService(&grpcstub.ServiceDesc{ServiceName: "` + svc + `"}, srv)
}
`
}

func todo13Write(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// todo13ProtoRepo 生成带共享 proto 复制品的仓库骨架。
func todo13ProtoRepo(t *testing.T, dir, module, gomodExtra, body string) {
	t.Helper()
	todo13Write(t, dir, "go.mod", "module "+module+"\n\ngo 1.21\n\nrequire example.com/proto v0.0.0\n\nreplace example.com/proto => ./proto\n"+gomodExtra)
	todo13Write(t, dir, "proto/go.mod", "module example.com/proto\n\ngo 1.21\n")
	todo13Write(t, dir, "proto/grpcstub/stub.go", todo13ProtoStub)
	todo13Write(t, dir, "proto/ci/ci.pb.go", todo13ProtoSvc("ci", "ci.RevalidationOrderService", "CI"))
	todo13Write(t, dir, "proto/other/other.pb.go", todo13ProtoSvc("other", "other.RevalidationOrderService", "Other"))
	todo13Write(t, dir, "app/app.go", body)
}

func TestTodo13ProtoIdentityRealIndex(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	root := t.TempDir()
	clientDir := filepath.Join(root, "client")
	srvA := filepath.Join(root, "server-a")
	srvB := filepath.Join(root, "server-b")

	todo13ProtoRepo(t, clientDir, "example.com/client", "", `package app

import (
	"context"

	"example.com/proto/ci"
	"example.com/proto/other"
)

type Gateway struct {
	a ci.RevalidationOrderServiceClient
	b other.RevalidationOrderServiceClient
}

func (g *Gateway) SubmitA(ctx context.Context, req *ci.CIRequest) (*ci.CIResponse, error) {
	return g.a.Submit(ctx, req)
}

func (g *Gateway) SubmitB(ctx context.Context, req *other.OtherRequest) (*other.OtherResponse, error) {
	return g.b.Submit(ctx, req)
}

func (g *Gateway) CancelA(ctx context.Context, req *ci.CIRequest) (*ci.CIResponse, error) {
	return g.a.Cancel(ctx, req)
}
`)
	todo13ProtoRepo(t, srvA, "example.com/server-a", "", `package app

import (
	"context"
	"net/http"

	"example.com/proto/ci"
	"example.com/proto/grpcstub"
)

type ImplA struct{}

func (ImplA) Submit(ctx context.Context, in *ci.CIRequest) (*ci.CIResponse, error) {
	_, _ = http.Get("https://notify.example.test/notify")
	return &ci.CIResponse{OK: true}, nil
}

func (ImplA) Cancel(ctx context.Context, in *ci.CIRequest) (*ci.CIResponse, error) {
	return &ci.CIResponse{OK: true}, nil
}

func SetupA(reg grpcstub.ServiceRegistrar) {
	ci.RegisterRevalidationOrderServiceServer(reg, ImplA{})
}
`)
	todo13ProtoRepo(t, srvB, "example.com/server-b", "", `package app

import (
	"context"
	"net/http"

	"example.com/proto/grpcstub"
	"example.com/proto/other"
)

type ImplB struct{}

func (ImplB) Submit(ctx context.Context, in *other.OtherRequest) (*other.OtherResponse, error) {
	_, _ = http.Get("https://other.example.test/notify")
	return &other.OtherResponse{OK: true}, nil
}

func (ImplB) Cancel(ctx context.Context, in *other.OtherRequest) (*other.OtherResponse, error) {
	return &other.OtherResponse{OK: true}, nil
}

func SetupB(reg grpcstub.ServiceRegistrar) {
	other.RegisterRevalidationOrderServiceServer(reg, ImplB{})
}
`)

	for _, d := range []string{clientDir, srvA, srvB} {
		clearIndex(t, d)
		if code := runCLI(t, "init", "--repo", d, "--progress", "none"); code != 0 {
			t.Fatalf("init %s exit = %d", d, code)
		}
	}

	open := func(p string) (*action.Actions, func() error, error) {
		db, err := sqlite.Open(p)
		if err != nil {
			return nil, nil, err
		}
		return action.New(sqlite.NewRepo(db)), db.Close, nil
	}
	clientDB, err := sqlite.Open(clientDir)
	if err != nil {
		t.Fatal(err)
	}
	defer clientDB.Close()
	acts := action.New(sqlite.NewRepo(clientDB))

	type want struct {
		repo    string
		svcID   string
		next    string
		match   string
	}
	cases := []struct {
		name   string
		symbol string
		orders [][]string
		want   want
	}{
		{"SubmitA", "symbol:go:example.com/client/app:(Gateway).SubmitA",
			[][]string{{srvA, srvB}, {srvB, srvA}},
			want{srvA, "symbol:go:example.com/proto/ci:svc.RevalidationOrderService", "notify.example.test", "proto-identity"}},
		{"SubmitB", "symbol:go:example.com/client/app:(Gateway).SubmitB",
			[][]string{{srvA, srvB}, {srvB, srvA}},
			want{srvB, "symbol:go:example.com/proto/other:svc.RevalidationOrderService", "other.example.test", "proto-identity"}},
	}
	for _, tc := range cases {
		for oi, cands := range tc.orders {
			node, err := acts.ExtChain(action.ExtChainRequest{Symbol: tc.symbol, RepoAbs: clientDir,
				CandidateRepos: cands, Open: open})
			if err != nil {
				t.Fatal(err)
			}
			if len(node.Grpc) != 1 {
				t.Fatalf("%s order%d: 出站=%d", tc.name, oi, len(node.Grpc))
			}
			g := node.Grpc[0]
			if g.Stop != nil {
				t.Fatalf("%s order%d: 应续接成功: %+v", tc.name, oi, g.Stop)
			}
			if g.TargetRepo != tc.want.repo || g.TargetServiceID != tc.want.svcID || g.MatchReason != tc.want.match {
				t.Errorf("%s order%d: target=%s svc=%s reason=%s; want %s / %s / %s",
					tc.name, oi, g.TargetRepo, g.TargetServiceID, g.MatchReason,
					tc.want.repo, tc.want.svcID, tc.want.match)
			}
			var hosts []string
			for _, s := range g.Server {
				for _, h := range s.HTTP {
					hosts = append(hosts, h.Service)
				}
			}
			if len(hosts) != 1 || hosts[0] != tc.want.next {
				t.Errorf("%s order%d: 后续外部目标=%v want [%s]", tc.name, oi, hosts, tc.want.next)
			}
		}
	}

	// CancelA 不得串到 Submit 的下游（方法身份独立）
	node, err := acts.ExtChain(action.ExtChainRequest{
		Symbol: "symbol:go:example.com/client/app:(Gateway).CancelA", RepoAbs: clientDir,
		CandidateRepos: []string{srvA, srvB}, Open: open})
	if err != nil {
		t.Fatal(err)
	}
	if len(node.Grpc) != 1 || node.Grpc[0].Method != "Cancel" {
		t.Fatalf("CancelA 应只有 Cancel 出站: %+v", node.Grpc)
	}
	for _, s := range node.Grpc[0].Server {
		for _, h := range s.HTTP {
			if strings.Contains(h.Service, "notify") {
				t.Errorf("Cancel 不得混入 Submit 的下游: %+v", h)
			}
		}
	}
	// JSON 契约可读（source/target 完整服务身份）
	raw, _ := json.Marshal(node.Grpc[0])
	if !strings.Contains(string(raw), "source_service_id") || !strings.Contains(string(raw), "target_service_id") {
		t.Errorf("JSON 应含完整服务身份: %s", raw)
	}
}
