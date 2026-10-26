//go:build integration

package integration

// todo19-7：认领既有（已构建）worktree 保留构建元数据，ext-chain 候选不丢。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

const todo19ClaimStub = `package stub

type ServiceDesc struct{ ServiceName string }

type ServiceRegistrar interface {
	RegisterService(desc *ServiceDesc, impl any)
}
`

const todo19ClaimProto = `package claim

import (
	"context"

	"example.com/claimproto/stub"
)

type Req struct{ ID string }
type Resp struct{ OK bool }

type ClaimServiceClient interface {
	Submit(ctx context.Context, in *Req, opts ...any) (*Resp, error)
}

type claimClient struct{}

func (c *claimClient) Submit(ctx context.Context, in *Req, opts ...any) (*Resp, error) {
	return &Resp{OK: true}, nil
}

func NewClaimServiceClient() ClaimServiceClient { return &claimClient{} }

type ClaimServiceServer interface {
	Submit(ctx context.Context, in *Req) (*Resp, error)
}

func RegisterClaimServiceServer(s stub.ServiceRegistrar, srv ClaimServiceServer) {
	s.RegisterService(&stub.ServiceDesc{ServiceName: "claim.ClaimService"}, srv)
}
`

func todo19WriteFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func todo19MakeRepo(t *testing.T, dir, module, body string) {
	t.Helper()
	todo19WriteFile(t, dir, "go.mod", "module "+module+"\n\ngo 1.21\n\nrequire example.com/claimproto v0.0.0\n\nreplace example.com/claimproto => ./proto\n")
	todo19WriteFile(t, dir, "proto/go.mod", "module example.com/claimproto\n\ngo 1.21\n")
	todo19WriteFile(t, dir, "proto/stub/stub.go", todo19ClaimStub)
	todo19WriteFile(t, dir, "proto/claim/claim.pb.go", todo19ClaimProto)
	todo19WriteFile(t, dir, "app/app.go", body)
	if gitAvailable() {
		runGit(t, dir, "init", "-b", "main", ".")
		runGit(t, dir, "add", ".")
		runGit(t, dir, "commit", "-m", "init")
	}
}

func todo19RegistryRepo(t *testing.T, path string) sqlite.RegistryRepo {
	t.Helper()
	r, err := sqlite.OpenRegistry(os.Getenv("CODEINTEL_REGISTRY_DIR"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	rp, ok, err := r.FindRepo(path)
	if err != nil || !ok {
		t.Fatalf("注册条目缺失 %s: ok=%v err=%v", path, ok, err)
	}
	return rp
}

func TestTodo19WorkspaceClaimKeepsExtChain(t *testing.T) {
	if !scipGoAvailable() || !gitAvailable() {
		t.Skip("scip-go/git not found")
	}
	root := t.TempDir()
	mainClient := filepath.Join(root, "client-main")
	mainServer := filepath.Join(root, "server-main")
	ws := filepath.Join(root, "workspace")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	todo19MakeRepo(t, mainClient, "example.com/claimclient", `package app

import (
	"context"

	"example.com/claimproto/claim"
)

type Gateway struct{ c claim.ClaimServiceClient }

func (g *Gateway) Submit(ctx context.Context, req *claim.Req) (*claim.Resp, error) {
	return g.c.Submit(ctx, req)
}
`)
	todo19MakeRepo(t, mainServer, "example.com/claimserver", `package app

import (
	"context"
	"net/http"

	"example.com/claimproto/claim"
	"example.com/claimproto/stub"
)

type ClaimImpl struct{}

func (ClaimImpl) Submit(ctx context.Context, in *claim.Req) (*claim.Resp, error) {
	_, _ = http.Get("https://claim-target.example.test/notify")
	return &claim.Resp{OK: true}, nil
}

func Setup(reg stub.ServiceRegistrar) {
	claim.RegisterClaimServiceServer(reg, ClaimImpl{})
}
`)
	wtClient := filepath.Join(ws, "client")
	wtServer := filepath.Join(ws, "server")
	runGit(t, mainClient, "worktree", "add", "--detach", wtClient)
	runGit(t, mainServer, "worktree", "add", "--detach", wtServer)
	for _, d := range []string{wtClient, wtServer} {
		clearIndex(t, d)
		if code := runCLI(t, "init", "--repo", d, "--progress", "none"); code != 0 {
			t.Fatalf("init %s exit=%d", d, code)
		}
	}
	beforeClient := todo19RegistryRepo(t, wtClient)
	beforeServer := todo19RegistryRepo(t, wtServer)
	if beforeClient.BuildID == "" || beforeServer.BuildID == "" {
		t.Fatalf("前置 init 应有 BuildID: %+v %+v", beforeClient, beforeServer)
	}

	chain := func() action.ExtChainNode {
		t.Helper()
		code, out := runCLIOut(t, "query", "ext-chain",
			"symbol:go:example.com/claimclient/app:(Gateway).Submit", "--repo", wtClient, "--json")
		if code != 0 {
			t.Fatalf("ext-chain exit=%d; %s", code, out)
		}
		var node action.ExtChainNode
		if err := json.Unmarshal([]byte(out), &node); err != nil {
			t.Fatalf("ext-chain JSON: %v", err)
		}
		return node
	}
	verifyChain := func(label string, node action.ExtChainNode) {
		t.Helper()
		if len(node.Grpc) != 1 || node.Grpc[0].Stop != nil {
			t.Fatalf("%s: gRPC 未续接: %+v", label, node.Grpc)
		}
		g := node.Grpc[0]
		if g.TargetRepo != wtServer {
			t.Errorf("%s: target_repo=%q want %q", label, g.TargetRepo, wtServer)
		}
		if !strings.Contains(g.TargetMethod, "ClaimImpl") {
			t.Errorf("%s: target_method=%q 应含 ClaimImpl", label, g.TargetMethod)
		}
		found := false
		for _, s := range g.Server {
			for _, h := range s.HTTP {
				if h.Service == "claim-target.example.test" {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("%s: 服务端下游应含 claim-target: %+v", label, g.Server)
		}
	}
	verifyChain("before-claim", chain())
	// list 投影认领前后均为已构建
	listStatus := func(label string) map[string]string {
		t.Helper()
		code, out := runCLIOut(t, "list", "--json")
		if code != 0 {
			t.Fatalf("%s list exit=%d; %s", label, code, out)
		}
		var entries []map[string]any
		if err := json.Unmarshal([]byte(out), &entries); err != nil {
			t.Fatalf("%s list JSON: %v", label, err)
		}
		got := map[string]string{}
		for _, e := range entries {
			p, _ := e["path"].(string)
			st, _ := e["status"].(string)
			if p != "" {
				got[p] = st
			}
		}
		return got
	}
	beforeStatus := listStatus("before-claim")
	if beforeStatus[wtClient] != "已构建" || beforeStatus[wtServer] != "已构建" {
		t.Fatalf("认领前应已构建: %+v", beforeStatus)
	}

	// 官方认领（主仓库未登记 → git worktree 元数据证明）
	out := captureStdoutIntegration(t, func() {
		if code := runCLI(t, "workspace", "init", "--dir", ws,
			"--repo", mainClient, "--repo", mainServer); code != 0 {
			t.Errorf("workspace init claim exit=%d", code)
		}
	})
	afterClient := todo19RegistryRepo(t, wtClient)
	afterServer := todo19RegistryRepo(t, wtServer)
	if afterClient.BuildID != beforeClient.BuildID || afterServer.BuildID != beforeServer.BuildID ||
		afterClient.LastBuiltAt != beforeClient.LastBuiltAt || afterServer.LastBuiltAt != beforeServer.LastBuiltAt ||
		afterClient.HeadCommit != beforeClient.HeadCommit || afterServer.HeadCommit != beforeServer.HeadCommit {
		t.Errorf("认领不得丢失构建元数据:\nbefore=%+v/%+v\nafter =%+v/%+v\n%s",
			beforeClient, beforeServer, afterClient, afterServer, out)
	}
	if afterClient.Workspace != ws || afterServer.Workspace != ws {
		t.Errorf("workspace 归属未恢复: %+v %+v", afterClient, afterServer)
	}
	afterStatus := listStatus("after-claim")
	if afterStatus[wtClient] != "已构建" || afterStatus[wtServer] != "已构建" {
		t.Errorf("认领后仍应已构建（BuildID/LasBuiltAt 保留）: %+v", afterStatus)
	}
	verifyChain("after-claim", chain())
}

// captureStdoutIntegration 捕获 in-process CLI stdout（避免与其它测试辅助重名）。
func captureStdoutIntegration(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	f()
	w.Close()
	os.Stdout = old
	buf := make([]byte, 0, 1024)
	tmp := make([]byte, 1024)
	for {
		n, e := r.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if e != nil {
			break
		}
	}
	return string(buf)
}
