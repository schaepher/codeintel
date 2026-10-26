package action

// todo11-1 验收：同一函数依次调用同一服务的 Submit 与 Cancel——索引与查询
// 同时保留两个方法，各自仅续接对应实现（此前被 edges 唯一约束合并，chain
// 只剩一个方法）。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedMultiMethodClient 单函数调同服务两个 RPC 方法（Key 区分）。
func seedMultiMethodClient(t *testing.T) *extRepo {
	t.Helper()
	dir := t.TempDir()
	if err := writeGoMod(dir); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/cli:(D).Run", Kind: domain.KindMethod, Name: "(D).Run", FilePath: "d.go", LineStart: 5},
		{ID: "symbol:go:example.com/cli:svc.OrderService", Kind: domain.KindGrpcService,
			Name: "svc.OrderService", FilePath: "d.go", Properties: map[string]any{"service_name": "OrderService"}},
	}, []*domain.Fact{
		{SourceID: "symbol:go:example.com/cli:(D).Run", TargetID: "symbol:go:example.com/cli:svc.OrderService",
			Kind: domain.FactGrpcCall, Key: "Submit", Confidence: 1.0, Metadata: map[string]any{"method": "Submit", "line_num": 6}},
		{SourceID: "symbol:go:example.com/cli:(D).Run", TargetID: "symbol:go:example.com/cli:svc.OrderService",
			Kind: domain.FactGrpcCall, Key: "Cancel", Confidence: 1.0, Metadata: map[string]any{"method": "Cancel", "line_num": 7}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "multi1", ToolName: "all", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	return &extRepo{dir: dir, acts: New(r), db: db}
}

// TestExtChainSameCallerTwoMethods：一个调用方的两个 RPC 方法互不混淆。
func TestExtChainSameCallerTwoMethods(t *testing.T) {
	client := seedMultiMethodClient(t)
	defer client.close()
	server := seedExtServerRepo(t)
	defer server.close()

	root, err := client.acts.ExtChain(ExtChainRequest{
		Symbol: "symbol:go:example.com/cli:(D).Run", RepoAbs: client.dir,
		CandidateRepos: []string{server.dir}, Open: testRepoOpener(server.acts),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(root.Grpc) != 2 {
		t.Fatalf("同调用方两个 RPC 方法应各有条目（不合并），实为 %d: %+v", len(root.Grpc), root.Grpc)
	}
	byMethod := map[string]ExtChainGrpc{}
	for _, g := range root.Grpc {
		byMethod[g.Method] = g
	}
	for method, wantTarget := range map[string]string{"Submit": ":(orderImpl).Submit", "Cancel": ":(orderImpl).Cancel"} {
		g, ok := byMethod[method]
		if !ok {
			t.Fatalf("缺 %s 出站调用: %+v", method, root.Grpc)
		}
		if g.Stop != nil {
			t.Fatalf("%s 应续接成功: %+v", method, g.Stop)
		}
		if !strings.HasSuffix(g.TargetMethod, wantTarget) {
			t.Errorf("%s 目标方法 = %q; want 后缀 %q", method, g.TargetMethod, wantTarget)
		}
		// 续接的下游只能是对应方法的（不混入另一个方法）
		for _, s := range g.Server {
			downstream := []string{}
			for _, h := range s.HTTP {
				downstream = append(downstream, h.Service)
			}
			got := strings.Join(downstream, ",")
			switch method {
			case "Submit":
				if got != "sms.example.com" {
					t.Errorf("Submit 下游 = %q; want sms.example.com（不混入 Cancel 的 mail）", got)
				}
			case "Cancel":
				if got != "mail.example.com" {
					t.Errorf("Cancel 下游 = %q; want mail.example.com（不混入 Submit 的 sms）", got)
				}
			}
		}
	}
}

// TestGrpcCallsPerMethodIndexed：索引层真相——两条边、方法各自可见
// （防止回退到"按服务合并"）。
func TestGrpcCallsPerMethodIndexed(t *testing.T) {
	client := seedMultiMethodClient(t)
	defer client.close()
	facts, err := client.acts.repo.GetFactsByKinds(string(domain.FactGrpcCall))
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for _, f := range facts {
		keys[f.Key] = true
	}
	if len(facts) != 2 || !keys["Submit"] || !keys["Cancel"] {
		t.Fatalf("索引应保留两个 RPC 方法身份: %+v", facts)
	}
}
