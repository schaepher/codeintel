package action

// todo10-4：ext-chain 的明确诊断（方法缺失 / 目标仓库不可用 / 循环）。
// 从 action_ext_chain_test.go 拆出（行数治理）。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// TestExtChainDiagnostics：方法缺失 / 目标仓库不可用 都有明确诊断。
func TestExtChainDiagnostics(t *testing.T) {
	client := seedExtClientRepo(t)
	defer client.close()
	server := seedExtServerRepo(t)
	defer server.close()

	// 方法缺失：另一个调用方调 OrderService.Refund（服务端没有）——
	// 用不同调用方，避免与 Run 的 Submit 边合并（UNIQUE(source,target,kind)）
	if _, err := sqlite.NewRepo(client.db).SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/cli:(D).RefundJob", Kind: domain.KindMethod, Name: "(D).RefundJob", FilePath: "d.go", LineStart: 20},
	}, []*domain.Fact{
		{SourceID: "symbol:go:example.com/cli:(D).RefundJob", TargetID: "symbol:go:example.com/cli:svc.OrderService",
			Kind: domain.FactGrpcCall, Confidence: 1.0, Metadata: map[string]any{"method": "Refund", "line_num": 21}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	client.acts = New(sqlite.NewRepo(client.db))
	// 缓存按 build_id 失效：换 build_id 强制重算
	if err := sqlite.NewRepo(client.db).Save(&domain.BuildMeta{BuildID: "cli2", ToolName: "all", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	root, err := client.acts.ExtChain(ExtChainRequest{
		Symbol: "symbol:go:example.com/cli:(D).RefundJob", RepoAbs: client.dir,
		CandidateRepos: []string{server.dir}, Open: testRepoOpener(server.acts),
	})
	if err != nil {
		t.Fatal(err)
	}
	var refund *ExtChainGrpc
	for i := range root.Grpc {
		if root.Grpc[i].Method == "Refund" {
			refund = &root.Grpc[i]
		}
	}
	if refund == nil {
		t.Fatalf("应有 Refund 出站调用: %+v", root.Grpc)
	}
	if refund.Stop == nil || refund.Stop.Code != ExtStopMethodNotFound {
		t.Fatalf("方法缺失应给 %q 诊断: %+v", ExtStopMethodNotFound, refund.Stop)
	}
	if !strings.Contains(refund.Stop.Detail, "Submit") {
		t.Errorf("诊断应列出该服务已实现的方法（便于发现名字不同）: %+v", refund.Stop)
	}

	// 目标仓库不可用：候选仓库里没有任何服务端实现
	root2, err := client.acts.ExtChain(ExtChainRequest{
		Symbol: "symbol:go:example.com/cli:(D).RefundJob", RepoAbs: client.dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	var unreachable bool
	for _, g := range root2.Grpc {
		if g.Stop != nil && g.Stop.Code == ExtStopRepoMissing {
			unreachable = true
		}
	}
	if !unreachable {
		t.Errorf("无实现仓库时应给 %q 诊断: %+v", ExtStopRepoMissing, root2.Grpc)
	}
}

// TestExtChainCycle：服务方法互相调用（A→B→A）→ 循环诊断。
func TestExtChainCycle(t *testing.T) {
	dir := t.TempDir()
	if err := writeGoMod(dir); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/srv:svc.OrderService", Kind: domain.KindGrpcService,
			Name: "svc.OrderService", FilePath: "srv.go",
			Properties: map[string]any{"service_name": "OrderService", "methods": "Submit,Cancel"}},
		{ID: "symbol:go:example.com/srv:svc.OtherService", Kind: domain.KindGrpcService,
			Name: "svc.OtherService", FilePath: "srv.go",
			Properties: map[string]any{"service_name": "OtherService"}},
		{ID: "symbol:go:example.com/srv:orderImpl", Kind: domain.KindStruct, Name: "orderImpl", FilePath: "srv.go", LineStart: 3},
		{ID: "symbol:go:example.com/srv:(orderImpl).Submit", Kind: domain.KindMethod,
			Name: "(orderImpl).Submit", FilePath: "srv.go", LineStart: 5},
		{ID: "symbol:go:example.com/srv:(orderImpl).Cancel", Kind: domain.KindMethod,
			Name: "(orderImpl).Cancel", FilePath: "srv.go", LineStart: 9},
	}, []*domain.Fact{
		{SourceID: "symbol:go:example.com/srv:orderImpl", TargetID: "symbol:go:example.com/srv:svc.OrderService",
			Kind: domain.FactGrpcImpl, Confidence: 1.0},
		// Submit → Cancel → Submit（循环）
		{SourceID: "symbol:go:example.com/srv:(orderImpl).Submit", TargetID: "symbol:go:example.com/srv:svc.OrderService",
			Kind: domain.FactGrpcCall, Confidence: 1.0, Metadata: map[string]any{"method": "Cancel", "line_num": 6}},
		{SourceID: "symbol:go:example.com/srv:(orderImpl).Cancel", TargetID: "symbol:go:example.com/srv:svc.OrderService",
			Kind: domain.FactGrpcCall, Confidence: 1.0, Metadata: map[string]any{"method": "Submit", "line_num": 10}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "cyc1", ToolName: "all", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	acts := New(r)
	root, err := acts.ExtChain(ExtChainRequest{Symbol: "symbol:go:example.com/srv:(orderImpl).Submit", RepoAbs: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(root.Grpc) == 0 {
		t.Fatalf("应有出站 grpc: %+v", root)
	}
	// 链：Submit → Cancel → Submit →（再到 Cancel 时循环停止）
	var sawCycle bool
	var walk func(n *ExtChainNode)
	walk = func(n *ExtChainNode) {
		for _, g := range n.Grpc {
			if g.Stop != nil && g.Stop.Code == ExtStopCycle {
				sawCycle = true
			}
			for i := range g.Server {
				walk(&g.Server[i])
			}
		}
	}
	walk(root)
	if !sawCycle {
		t.Errorf("循环调用应给 %q 诊断: %+v", ExtStopCycle, root)
	}
}

// testRepoOpener 返回固定仓库视图的 opener（避免测试打开真实 DB）。
func testRepoOpener(acts *Actions) ExtChainRepoOpener {
	return func(repoPath string) (*Actions, func() error, error) {
		return acts, nil, nil
	}
}
