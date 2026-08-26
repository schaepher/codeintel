package cli

// R92 测试：cmdGrpcRoutes 转发（查询逻辑在 action——Actions.GrpcRoutes
// 已单独测试）；cli 只做参数转发 + 输出。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// TestCmdGrpcRoutesForward：命令行转发——JSON 输出含服务契约字段。
func TestCmdGrpcRoutesForward(t *testing.T) {
	dir := seedRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m/grpc:svc.QueryService", Kind: domain.KindGrpcService,
			Name: "svc.QueryService", FilePath: "grpc/query_grpc.pb.go",
			Properties: map[string]any{"service_name": "QueryService", "methods": "Query"}},
	}, []*domain.Fact{
		{SourceID: "symbol:go:example.com/m/impl:queryServiceImpl", TargetID: "symbol:go:example.com/m/grpc:svc.QueryService",
			Kind: domain.FactGrpcImpl, Confidence: 1.0},
	}, nil); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(func() {
		if code := cmdGrpcRoutes(action.New(sqlite.NewRepo(db)), dir, queryFlags{json: true}); code != 0 {
			t.Fatalf("cmdGrpcRoutes exit = %d", code)
		}
	})
	for _, want := range []string{`"services"`, `"name": "QueryService"`, `"methods"`} {
		if !strings.Contains(out, want) {
			t.Errorf("命令输出应含 %s:\n%s", want, out)
		}
	}
}
