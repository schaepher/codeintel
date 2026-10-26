package action

// todo12-2：跨仓续接期间保持目标数据库可用。
//   - 目标查询视图的所有权保留到递归结束（此前 defer 关闭 → server 链为空）
//   - 目标读取失败给 query_failed 诊断 + partial，不静默变成“无下游”
//   - 合法无下游保持 Stop=nil / Partial=false（与读取失败分开）

import (
	"errors"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// TestExtChainTargetViewLifetime：真实 DB 打开/关闭——递归期间目标库仍可用，
// server 链完整，且关闭恰好一次（不重复关闭、不泄漏）。
func TestExtChainTargetViewLifetime(t *testing.T) {
	client := seedExtClientRepo(t)
	defer client.close()
	server := seedExtServerRepo(t)
	defer server.close()

	opens, closes := 0, 0
	opener := func(repoPath string) (*Actions, func() error, error) {
		db, err := sqlite.Open(repoPath)
		if err != nil {
			return nil, nil, err
		}
		opens++
		return New(sqlite.NewRepo(db)), func() error {
			closes++
			return db.Close()
		}, nil
	}
	root, err := client.acts.ExtChain(ExtChainRequest{
		Symbol: "symbol:go:example.com/cli:(D).Run", RepoAbs: client.dir,
		CandidateRepos: []string{server.dir}, Open: opener,
	})
	if err != nil {
		t.Fatal(err)
	}
	if opens != 1 || closes != 1 {
		t.Fatalf("目标查询视图应打开/关闭各一次，实际 open=%d close=%d", opens, closes)
	}
	var submit *ExtChainGrpc
	for i := range root.Grpc {
		if root.Grpc[i].Method == "Submit" {
			submit = &root.Grpc[i]
		}
	}
	if submit == nil {
		t.Fatalf("应有 Submit 出站调用: %+v", root.Grpc)
	}
	if submit.Stop != nil || submit.Partial {
		t.Fatalf("Submit 应完整续接: stop=%+v partial=%v", submit.Stop, submit.Partial)
	}
	hosts := map[string]bool{}
	for _, s := range submit.Server {
		for _, h := range s.HTTP {
			hosts[h.Service] = true
		}
	}
	if !hosts["sms.example.com"] {
		t.Fatalf("server 应包含 Submit 的下游 sms：%+v", submit.Server)
	}
	if hosts["mail.example.com"] {
		t.Errorf("Submit 不得混入 Cancel 的下游 mail：%+v", submit.Server)
	}
}

// TestExtChainLegalNoDownstream：目标方法存在但没有出站调用——合法空链，
// 不能标 partial/stop（与读取失败区分）。
func TestExtChainLegalNoDownstream(t *testing.T) {
	client := seedExtClientRepo(t)
	defer client.close()
	server := seedExtServerRepo(t)
	defer server.close()

	root, err := client.acts.ExtChain(ExtChainRequest{
		Symbol: "symbol:go:example.com/cli:(D).CancelJob", RepoAbs: client.dir,
		CandidateRepos: []string{server.dir}, Open: testRepoOpener(server.acts),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(root.Grpc) == 0 {
		t.Fatalf("应有 Cancel 出站: %+v", root.Grpc)
	}
	g := root.Grpc[0]
	if g.Stop != nil || g.Partial {
		t.Fatalf("合法续接不得带停止/partial 诊断: %+v", g)
	}
	for _, s := range g.Server {
		for _, h := range s.HTTP {
			if h.Service != "mail.example.com" {
				t.Errorf("Cancel 下游应为 mail，实为 %s", h.Service)
			}
		}
	}
}

// TestExtChainTargetQueryFailure：目标仓库视图读不到（DB 已关闭）→
// query_failed + 非空诊断，不伪装成“无下游”。
func TestExtChainTargetQueryFailure(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlite.NewRepo(db).SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/srv:svc.OrderService", Kind: domain.KindGrpcService,
			Name: "svc.OrderService", FilePath: "srv.go",
			Properties: map[string]any{"service_name": "OrderService", "methods": "Submit"}},
		{ID: "symbol:go:example.com/srv:orderImpl", Kind: domain.KindStruct, Name: "orderImpl", FilePath: "srv.go", LineStart: 3},
		{ID: "symbol:go:example.com/srv:(orderImpl).Submit", Kind: domain.KindMethod,
			Name: "(orderImpl).Submit", FilePath: "srv.go", LineStart: 5},
	}, []*domain.Fact{
		{SourceID: "symbol:go:example.com/srv:orderImpl", TargetID: "symbol:go:example.com/srv:svc.OrderService",
			Kind: domain.FactGrpcImpl, Confidence: 1.0},
	}, nil); err != nil {
		t.Fatal(err)
	}
	if err := sqlite.NewRepo(db).Save(&domain.BuildMeta{BuildID: "srv-closed", ToolName: "all", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	acts := New(sqlite.NewRepo(db))
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	stop := probeTarget(&remoteTarget{acts: acts, repo: dir,
		methodID: "symbol:go:example.com/srv:(orderImpl).Submit"})
	if stop == nil || stop.Code != ExtStopQueryFailed {
		t.Fatalf("读取失败应给 %q 诊断，实为 %+v", ExtStopQueryFailed, stop)
	}
	if stop.Detail == "" {
		t.Errorf("诊断应带失败原因: %+v", stop)
	}
	d := seqDiagOf(stop)
	if d.Code != ExtStopQueryFailed || d.Detail == "" {
		t.Errorf("诊断转换应保留 code/detail: %+v", d)
	}
}

// TestExtChainOpenFailureDiagnostic：候选仓库打开失败时诊断要写明“不可用”
// （不能被当成“没有任何实现”）。
func TestExtChainOpenFailureDiagnostic(t *testing.T) {
	client := seedExtClientRepo(t)
	defer client.close()
	server := seedExtServerRepo(t)
	defer server.close()

	opens, closes := 0, 0
	root, err := client.acts.ExtChain(ExtChainRequest{
		Symbol: "symbol:go:example.com/cli:(D).Run", RepoAbs: client.dir,
		CandidateRepos: []string{server.dir},
		Open: func(string) (*Actions, func() error, error) {
			opens++
			return nil, func() error { closes++; return nil }, errors.New("dial timeout")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if opens != 1 || closes != 0 {
		t.Fatalf("打开失败不应产生关闭调用（open=%d close=%d）", opens, closes)
	}
	if len(root.Grpc) == 0 {
		t.Fatalf("应有出站调用: %+v", root)
	}
	if root.Grpc[0].Stop == nil || root.Grpc[0].Stop.Code != ExtStopRepoMissing {
		t.Fatalf("应给仓库不可用诊断: %+v", root.Grpc[0].Stop)
	}
}
