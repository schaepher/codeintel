//go:build integration

package integration

// todo25 §4.1：注册实参经 DI 工厂 + 类型断言时仍证明具体实现类型。

import (
	"testing"

	"github.com/schaepher/codeintel/internal/action"
)

const todo25MicroAssertApp = `package app

import (
	"context"

	"example.com/microflow/pb"
	micro "go-micro.dev/v2"
	"go-micro.dev/v2/client"
)

type AccountServer struct{}

func (h *AccountServer) Apply(ctx context.Context, in *pb.ApplyRequest, out *pb.ApplyResponse) error {
	SaveAudit()
	return nil
}

func SaveAudit() {}

func SaveOtherAudit() {}

type Container struct{}

func (c *Container) Get() *AccountServer { return new(AccountServer) }

func Setup() {
	svc := micro.NewService(micro.Name("example.flow.AccountService"))
	c := &Container{}
	pb.RegisterAccountServiceHandler(svc.Server(), c.Get().(pb.AccountServiceHandler))
}

func Entry(ctx context.Context, c client.Client) error {
	cli := pb.NewAccountServiceClient(c)
	_, err := cli.Apply(ctx, &pb.ApplyRequest{})
	return err
}
`

func TestTodo25MicroRegisterTypeAssertion(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	f := writeMicroFixture(t, map[string]string{"app.go": todo25MicroAssertApp})
	clearIndex(t, f.dir)
	if code := runCLI(t, "init", "--repo", f.dir, "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	root := seqTreeJSON(t, f.dir, "symbol:go:example.com/microflow:Entry", 6)
	rpc := requiredRPCNode(t, root)
	if rpc.Evidence.SymbolID != "symbol:go:example.com/microflow:(AccountServer).Apply" {
		t.Fatalf("注册实参类型断言应具体化到 AccountServer.Apply，got %+v", rpc.Evidence)
	}
	if rpc.Evidence.Source != action.SeqEvidenceRPC {
		t.Errorf("应为 RPC 续接证据，got %q", rpc.Evidence.Source)
	}
	if countSeqSym(root, ":SaveAudit") != 1 {
		t.Errorf("服务端实现应执行一次，got %d", countSeqSym(root, ":SaveAudit"))
	}
}
