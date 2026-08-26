package cli

// R71 测试：渲染基准告知（prompt）、facts token 优化（grpc 方法名
// 截断）、service 方法数（token 省）。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// TestDomainPromptRenderLimit：R71——prompt 告知渲染基准（域内实体数/
// 调用边 500 上限——AI 输出 domains 时自带子域划分与过大判断）。
func TestDomainPromptRenderLimit(t *testing.T) {
	p := domainPrompt("facts.json", "")
	for _, want := range []string{"渲染", "500", "实体数"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt 应含渲染基准 %q", want)
		}
	}
}

// TestDomainFactsGrpcMethodsTrim：R71——grpc 服务方法名截断（前 20 +
// 总数——MemberService 100+ 方法全量是 token 大头；AI 多域归属判断
// 不需要全部方法名）。
func TestDomainFactsGrpcMethodsTrim(t *testing.T) {
	dir := seedRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	methods := make([]string, 0, 25)
	for i := 0; i < 25; i++ {
		methods = append(methods, "Method"+itoa(i))
	}
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m/grpc:svc.BigService", Kind: domain.KindGrpcService,
			Name: "svc.BigService", FilePath: "grpc/big_grpc.pb.go",
			Properties: map[string]any{"service_name": "BigService", "methods": strings.Join(methods, ",")}},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	f := collectDomainFacts(action.New(r), dir, wikiConfig{})
	if len(f.Svcs) != 1 {
		t.Fatalf("服务数 = %d; want 1", len(f.Svcs))
	}
	s := f.Svcs[0]
	// 20 个方法名 + 1 个计数标注（"…共 25 个"）= 21 元素
	if len(s.Methods) > 21 {
		t.Errorf("方法名截断后 = %d; want ≤21（20 方法 + 计数标注）", len(s.Methods))
	}
	if !strings.HasSuffix(s.Methods[len(s.Methods)-1], "…共 25 个") {
		t.Errorf("末尾应标注总数: %q", s.Methods[len(s.Methods)-1])
	}
	if s.Methods[0] != "Method0" || s.Methods[19] != "Method19" {
		t.Errorf("前 20 个应为方法名: %q … %q", s.Methods[0], s.Methods[19])
	}
}
