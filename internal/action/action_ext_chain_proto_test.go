package action

// todo13-16：不同 proto 包的同名服务不得串线（完整身份消歧）。
//   - gen-a(ci) / gen-b(other) 各实现 RevalidationOrderService
//   - client-a 的 Submit → server-a（下游 notify.example.test）
//   - client-b 的 Submit → server-b（下游 other.example.test）
//   - 交换候选顺序/查询先后结果不变
//   - 缺少完整身份 + 多候选 → partial 候选清单，不静默取首个

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

func seedProtoServer(t *testing.T, module, protoPkg, impl, host string) *extRepo {
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
	svcID := domain.CanonicalID("symbol:go:" + module + "/" + protoPkg + ":svc.RevalidationOrderService")
	implID := domain.CanonicalID("symbol:go:" + module + "/" + protoPkg + ":" + impl)
	submitID := domain.CanonicalID(GrpcMethodEntryID(string(implID), "Submit"))
	cancelID := domain.CanonicalID(GrpcMethodEntryID(string(implID), "Cancel"))
	nodes := []*domain.CodeEntity{
		{ID: svcID, Kind: domain.KindGrpcService, Name: "svc.RevalidationOrderService",
			FilePath: protoPkg + "/gen.pb.go",
			Properties: map[string]any{"service_name": "RevalidationOrderService", "methods": "Submit,Cancel"}},
		{ID: implID, Kind: domain.KindStruct, Name: impl,
			FilePath: protoPkg + "/server.go", LineStart: 3},
		{ID: submitID, Kind: domain.KindMethod, Name: "(" + impl + ").Submit",
			FilePath: protoPkg + "/server.go", LineStart: 5},
		{ID: cancelID, Kind: domain.KindMethod, Name: "(" + impl + ").Cancel",
			FilePath: protoPkg + "/server.go", LineStart: 9},
		{ID: domain.CanonicalID("symbol:http:" + host + ":route./notify"), Kind: domain.KindHTTPRoute,
			Name: "route./notify", FilePath: protoPkg + "/server.go",
			Properties: map[string]any{"path": "/notify"}},
	}
	facts := []*domain.Fact{
		{SourceID: implID, TargetID: svcID, Kind: domain.FactGrpcImpl, Confidence: 1.0},
		{SourceID: submitID, TargetID: domain.CanonicalID("symbol:http:" + host + ":route./notify"),
			Kind: domain.FactHTTPCall, Confidence: 1.0,
			Metadata: map[string]any{"host": host, "path": "/notify", "line_num": 6}},
	}
	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "proto-" + module, ToolName: "all", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	return &extRepo{dir: dir, acts: New(r), db: db}
}

// seedProtoClient 两个客户端调用同名服务但完整身份不同。
func seedProtoClient(t *testing.T) *extRepo {
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
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/client:(ClientA).Run", Kind: domain.KindMethod,
			Name: "(ClientA).Run", FilePath: "client.go", LineStart: 5},
		{ID: "symbol:go:example.com/client:(ClientB).Run", Kind: domain.KindMethod,
			Name: "(ClientB).Run", FilePath: "client.go", LineStart: 12},
		{ID: "symbol:go:example.com/gen-a/ci:svc.RevalidationOrderService",
			Kind: domain.KindGrpcService, Name: "svc.RevalidationOrderService",
			Properties: map[string]any{"service_name": "RevalidationOrderService"}},
		{ID: "symbol:go:example.com/gen-b/other:svc.RevalidationOrderService",
			Kind: domain.KindGrpcService, Name: "svc.RevalidationOrderService",
			Properties: map[string]any{"service_name": "RevalidationOrderService"}},
	}
	facts := []*domain.Fact{
		{SourceID: "symbol:go:example.com/client:(ClientA).Run",
			TargetID: "symbol:go:example.com/gen-a/ci:svc.RevalidationOrderService",
			Kind: domain.FactGrpcCall, Confidence: 1.0,
			Metadata: map[string]any{"method": "Submit", "line_num": 6}},
		{SourceID: "symbol:go:example.com/client:(ClientB).Run",
			TargetID: "symbol:go:example.com/gen-b/other:svc.RevalidationOrderService",
			Kind: domain.FactGrpcCall, Confidence: 1.0,
			Metadata: map[string]any{"method": "Submit", "line_num": 13}},
	}
	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "proto-client", ToolName: "all", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	return &extRepo{dir: dir, acts: New(r), db: db}
}

func extGrpcByMethod(root *ExtChainNode, method string) ExtChainGrpc {
	for _, g := range root.Grpc {
		if g.Method == method {
			return g
		}
	}
	return ExtChainGrpc{}
}

func TestExtChainProtoIdentityDisambiguates(t *testing.T) {
	client := seedProtoClient(t)
	defer client.close()
	serverA := seedProtoServer(t, "example.com/gen-a", "ci", "implA", "notify.example.test")
	defer serverA.close()
	serverB := seedProtoServer(t, "example.com/gen-b", "other", "implB", "other.example.test")
	defer serverB.close()

	for _, tc := range []struct {
		name    string
		client  string
		cands   []string
		wantSrv *extRepo
		wantSvc string
		wantNext string
	}{
		{"a-first", "symbol:go:example.com/client:(ClientA).Run", []string{serverA.dir, serverB.dir},
			serverA, "symbol:go:example.com/gen-a/ci:svc.RevalidationOrderService", "notify.example.test"},
		{"a-swapped", "symbol:go:example.com/client:(ClientA).Run", []string{serverB.dir, serverA.dir},
			serverA, "symbol:go:example.com/gen-a/ci:svc.RevalidationOrderService", "notify.example.test"},
		{"b-first", "symbol:go:example.com/client:(ClientB).Run", []string{serverA.dir, serverB.dir},
			serverB, "symbol:go:example.com/gen-b/other:svc.RevalidationOrderService", "other.example.test"},
		{"b-swapped", "symbol:go:example.com/client:(ClientB).Run", []string{serverB.dir, serverA.dir},
			serverB, "symbol:go:example.com/gen-b/other:svc.RevalidationOrderService", "other.example.test"},
	} {
		open := ExtChainRepoOpener(func(p string) (*Actions, func() error, error) {
			switch p {
			case serverA.dir:
				return serverA.acts, func() error { return nil }, nil
			case serverB.dir:
				return serverB.acts, func() error { return nil }, nil
			}
			return nil, nil, nil
		})
		root, err := client.acts.ExtChain(ExtChainRequest{Symbol: tc.client, RepoAbs: client.dir,
			CandidateRepos: tc.cands, Open: open})
		if err != nil {
			t.Fatal(err)
		}
		g := extGrpcByMethod(root, "Submit")
		if g.Stop != nil {
			t.Fatalf("%s: Submit 应续接成功: %+v", tc.name, g.Stop)
		}
		if g.TargetRepo != tc.wantSrv.dir {
			t.Errorf("%s: target_repo=%q want %q", tc.name, g.TargetRepo, tc.wantSrv.dir)
		}
		if g.TargetServiceID != tc.wantSvc {
			t.Errorf("%s: target_service_id=%q want %q", tc.name, g.TargetServiceID, tc.wantSvc)
		}
		if g.MatchReason != "proto-identity" {
			t.Errorf("%s: match_reason=%q want proto-identity", tc.name, g.MatchReason)
		}
		var hosts []string
		for _, s := range g.Server {
			for _, h := range s.HTTP {
				hosts = append(hosts, h.Service)
			}
		}
		if len(hosts) != 1 || hosts[0] != tc.wantNext {
			t.Errorf("%s: 后续外部目标=%v want [%s]", tc.name, hosts, tc.wantNext)
		}
		// Cancel 不得串到 Submit 的结果
		if c := extGrpcByMethod(root, "Cancel"); c.TargetRepo == tc.wantSrv.dir && c.Method == "Cancel" {
			t.Errorf("%s: Cancel 不应共享 Submit 的唯一结论", tc.name)
		}
	}
}

// 缺少完整身份 + 多候选：列出候选 + partial，不静默选首个。
func TestExtChainProtoIdentityAmbiguous(t *testing.T) {
	client := seedProtoClient(t)
	defer client.close()
	serverA := seedProtoServer(t, "example.com/gen-a", "ci", "implA", "notify.example.test")
	defer serverA.close()
	serverB := seedProtoServer(t, "example.com/gen-b", "other", "implB", "other.example.test")
	defer serverB.close()

	// 调用侧身份不在候选范围（只保留短名）——ClientC 的边指向 client 包占位节点
	if _, err := sqlite.NewRepo(client.db).SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/client:(ClientC).Run", Kind: domain.KindMethod,
			Name: "(ClientC).Run", FilePath: "client.go", LineStart: 20},
		{ID: "symbol:go:example.com/client:svc.RevalidationOrderService",
			Kind: domain.KindGrpcService, Name: "svc.RevalidationOrderService",
			Properties: map[string]any{"service_name": "RevalidationOrderService"}},
	}, []*domain.Fact{
		{SourceID: "symbol:go:example.com/client:(ClientC).Run",
			TargetID: "symbol:go:example.com/client:svc.RevalidationOrderService",
			Kind: domain.FactGrpcCall, Confidence: 1.0,
			Metadata: map[string]any{"method": "Submit", "line_num": 21}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	open := ExtChainRepoOpener(func(p string) (*Actions, func() error, error) {
		switch p {
		case serverA.dir:
			return serverA.acts, func() error { return nil }, nil
		case serverB.dir:
			return serverB.acts, func() error { return nil }, nil
		}
		return nil, nil, nil
	})
	root, err := client.acts.ExtChain(ExtChainRequest{
		Symbol: "symbol:go:example.com/client:(ClientC).Run", RepoAbs: client.dir,
		CandidateRepos: []string{serverA.dir, serverB.dir}, Open: open})
	if err != nil {
		t.Fatal(err)
	}
	g := extGrpcByMethod(root, "Submit")
	if g.Stop == nil || g.Stop.Code != ExtStopServiceAmbiguous {
		t.Fatalf("多候选应报歧义而不是任选: %+v", g)
	}
	if !g.Partial {
		t.Errorf("歧义应标 partial: %+v", g)
	}
	for _, want := range []string{"example.com/gen-a/ci:svc.RevalidationOrderService",
		"example.com/gen-b/other:svc.RevalidationOrderService", "候选"} {
		if !strings.Contains(g.Stop.Detail, want) {
			t.Errorf("歧义诊断应列出 %q: %s", want, g.Stop.Detail)
		}
	}
	if g.TargetRepo != "" || g.TargetMethod != "" {
		t.Errorf("歧义时不得给出唯一目标: %+v", g)
	}
}
