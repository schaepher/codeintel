package action

// todo10-4：ext-chain 精确续接验收。
//   - 同一服务的 Submit 只续接 Submit（不混入 Cancel 的下游）
//   - 两个本地夹具仓库：客户端仓 → 服务端仓（经候选仓库解析）
//   - 方法缺失 / 目标仓库不可用 / 循环 / 深度上限都有 code 诊断

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// extRepo 一个夹具仓库的构建结果。
type extRepo struct {
	dir  string
	acts *Actions
	db   *sqlite.DB
}

func (r *extRepo) close() { _ = r.db.Close() }

// seedExtServerRepo 服务端仓库：OrderService 有 Submit 与 Cancel 两个方法，
// 各自调用不同下游（Submit → http sms；Cancel → http mail）。
func seedExtServerRepo(t *testing.T) *extRepo {
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
		{ID: "symbol:go:example.com/srv:svc.OrderService", Kind: domain.KindGrpcService,
			Name: "svc.OrderService", FilePath: "srv.go",
			Properties: map[string]any{"service_name": "OrderService", "methods": "Submit,Cancel"}},
		{ID: "symbol:go:example.com/srv:orderImpl", Kind: domain.KindStruct, Name: "orderImpl", FilePath: "srv.go", LineStart: 3},
		{ID: "symbol:go:example.com/srv:(orderImpl).Submit", Kind: domain.KindMethod,
			Name: "(orderImpl).Submit", FilePath: "srv.go", LineStart: 5},
		{ID: "symbol:go:example.com/srv:(orderImpl).Cancel", Kind: domain.KindMethod,
			Name: "(orderImpl).Cancel", FilePath: "srv.go", LineStart: 9},
		{ID: "symbol:http:sms.example.com:route./send", Kind: domain.KindHTTPRoute, Name: "route./send",
			FilePath: "srv.go", Properties: map[string]any{"path": "/send"}},
		{ID: "symbol:http:mail.example.com:route./send", Kind: domain.KindHTTPRoute, Name: "route./send",
			FilePath: "srv.go", Properties: map[string]any{"path": "/send"}},
	}, []*domain.Fact{
		{SourceID: "symbol:go:example.com/srv:orderImpl", TargetID: "symbol:go:example.com/srv:svc.OrderService",
			Kind: domain.FactGrpcImpl, Confidence: 1.0},
		{SourceID: "symbol:go:example.com/srv:(orderImpl).Submit", TargetID: "symbol:http:sms.example.com:route./send",
			Kind: domain.FactHTTPCall, Confidence: 1.0, Metadata: map[string]any{"host": "sms.example.com", "path": "/send"}},
		{SourceID: "symbol:go:example.com/srv:(orderImpl).Cancel", TargetID: "symbol:http:mail.example.com:route./send",
			Kind: domain.FactHTTPCall, Confidence: 1.0, Metadata: map[string]any{"host": "mail.example.com", "path": "/send"}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "srv1", ToolName: "all", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	return &extRepo{dir: dir, acts: New(r), db: db}
}

// seedExtClientRepo 客户端仓库：daemon.Run 调用 OrderService.Submit 与
// OrderService.Cancel 两个方法（grpc_call 边带 method 元数据）。
func seedExtClientRepo(t *testing.T) *extRepo {
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
		{ID: "symbol:go:example.com/cli:(D).CancelJob", Kind: domain.KindMethod, Name: "(D).CancelJob", FilePath: "d.go", LineStart: 12},
		{ID: "symbol:go:example.com/cli:svc.OrderService", Kind: domain.KindGrpcService,
			Name: "svc.OrderService", FilePath: "d.go", Properties: map[string]any{"service_name": "OrderService"}},
	}, []*domain.Fact{
		{SourceID: "symbol:go:example.com/cli:(D).Run", TargetID: "symbol:go:example.com/cli:svc.OrderService",
			Kind: domain.FactGrpcCall, Confidence: 1.0, Metadata: map[string]any{"method": "Submit", "line_num": 6}},
		{SourceID: "symbol:go:example.com/cli:(D).CancelJob", TargetID: "symbol:go:example.com/cli:svc.OrderService",
			Kind: domain.FactGrpcCall, Confidence: 1.0, Metadata: map[string]any{"method": "Cancel", "line_num": 13}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "cli1", ToolName: "all", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	return &extRepo{dir: dir, acts: New(r), db: db}
}

// TestExtChainPreciseMethod：Submit 只续接 Submit（不混入 Cancel 的下游）。
func TestExtChainPreciseMethod(t *testing.T) {
	client := seedExtClientRepo(t)
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
	byMethod := map[string]ExtChainGrpc{}
	for _, g := range root.Grpc {
		byMethod[g.Method] = g
	}
	submit, ok := byMethod["Submit"]
	if !ok {
		t.Fatalf("应有 Submit 出站调用: %+v", root.Grpc)
	}
	if submit.Stop != nil {
		t.Fatalf("Submit 应续接成功: %+v", submit.Stop)
	}
	if !strings.HasSuffix(submit.TargetMethod, ":(orderImpl).Submit") {
		t.Errorf("Submit 目标方法 = %q; want (orderImpl).Submit", submit.TargetMethod)
	}
	if submit.MatchReason != "proto-name" {
		t.Errorf("匹配依据 = %q; want proto-name", submit.MatchReason)
	}
	// 关键验收：Submit 的续接链里不能出现 Cancel 的下游（mail）
	for _, s := range submit.Server {
		for _, h := range s.HTTP {
			if h.Service == "mail.example.com" {
				t.Errorf("Submit 的续接链混入了 Cancel 的下游: %+v", s.HTTP)
			}
		}
	}
	// 另一调用方 CancelJob：只续接 Cancel（sms 不出现）——注意索引
	// UNIQUE(source,target,kind) 会把"同调用方到同服务不同方法"的
	// grpc_call 边合并成一条，故用不同调用方验证两个方向
	cancelRoot, err := client.acts.ExtChain(ExtChainRequest{
		Symbol: "symbol:go:example.com/cli:(D).CancelJob", RepoAbs: client.dir,
		CandidateRepos: []string{server.dir}, Open: testRepoOpener(server.acts),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cancelRoot.Grpc) == 0 || cancelRoot.Grpc[0].Method != "Cancel" {
		t.Fatalf("CancelJob 应只出站到 Cancel: %+v", cancelRoot.Grpc)
	}
	if !strings.HasSuffix(cancelRoot.Grpc[0].TargetMethod, ":(orderImpl).Cancel") {
		t.Errorf("Cancel 目标方法 = %q; want (orderImpl).Cancel", cancelRoot.Grpc[0].TargetMethod)
	}
	for _, s := range cancelRoot.Grpc[0].Server {
		for _, h := range s.HTTP {
			if h.Service == "sms.example.com" {
				t.Errorf("Cancel 的续接链混入了 Submit 的下游: %+v", s.HTTP)
			}
			if h.Service != "mail.example.com" {
				t.Errorf("Cancel 的下游应为 mail，实为 %s", h.Service)
			}
		}
	}
}

// TestExtChainCrossRepo：跨仓续接使用目标仓库路径（服务实现在候选仓库）。
func TestExtChainCrossRepo(t *testing.T) {
	client := seedExtClientRepo(t)
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
	if len(root.Grpc) == 0 {
		t.Fatalf("应有出站 grpc: %+v", root)
	}
	g := root.Grpc[0]
	if g.TargetRepo != server.dir {
		t.Errorf("TargetRepo = %q; want %q（跨仓解析目标仓库）", g.TargetRepo, server.dir)
	}
	if len(g.Server) != 1 || g.Server[0].Repo != server.dir {
		t.Errorf("递归应使用目标仓库视图: %+v", g.Server)
	}
}
