package action

// R92 测试（迁自 cli/wiki_processes_routes_test.go 与 wiki_processes_r55_test.go）：
// GrpcProcMethods——ImplID + 方法名构造 canonical ID 展开方法调用链；
// GrpcHandlerGoMethod——handler 名提取 Go 方法名（R55 小写 proto 方法名
// 恢复调用链）；无调用边方法文案不误标无效。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedGrpcProcRepo 预填 grpc 服务图：grpc_service 节点（methods 属性）+
// grpc_impl 边 + 实现类型方法 + 一级调用边；R55 小写 proto 方法名
// （sendCode → SendCode）场景 + Ping（节点存在但无调用边）。
func seedGrpcProcRepo(t *testing.T) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		// gRPC 服务 + 实现类型 + 方法
		{ID: "symbol:go:example.com/m/grpc:svc.QueryService", Kind: domain.KindGrpcService,
			Name: "svc.QueryService", FilePath: "grpc/query_grpc.pb.go",
			Properties: map[string]any{"service_name": "QueryService", "methods": "Query"}},
		{ID: "symbol:go:example.com/m/impl:queryServiceImpl", Kind: domain.KindStruct,
			Name: "queryServiceImpl", FilePath: "impl/query.go", LineStart: 20},
		{ID: "symbol:go:example.com/m/impl:(queryServiceImpl).Query", Kind: domain.KindMethod,
			Name: "(queryServiceImpl).Query", FilePath: "impl/query.go", LineStart: 25},
		{ID: "symbol:go:example.com/m/impl:queryHelper", Kind: domain.KindFunction,
			Name: "queryHelper", FilePath: "impl/query.go", LineStart: 30},
		// R55：ServiceDesc 方法名小写（proto 定义名 sendCode）→ 实现是
		// Go 导出名（SendCode）——handler 提取场景
		{ID: "symbol:go:example.com/m/impl:checkServiceImpl", Kind: domain.KindStruct,
			Name: "checkServiceImpl", FilePath: "impl/check.go", LineStart: 10},
		{ID: "symbol:go:example.com/m/impl:(checkServiceImpl).SendCode", Kind: domain.KindMethod,
			Name: "(checkServiceImpl).SendCode", FilePath: "impl/check.go", LineStart: 15},
		{ID: "symbol:go:example.com/m/impl:checkHelper", Kind: domain.KindFunction,
			Name: "checkHelper", FilePath: "impl/check.go", LineStart: 20},
		// Ping：节点存在但无项目内调用（健康检查形态）
		{ID: "symbol:go:example.com/m/impl:(checkServiceImpl).Ping", Kind: domain.KindMethod,
			Name: "(checkServiceImpl).Ping", FilePath: "impl/check.go", LineStart: 30},
	}
	if _, err := r.SaveBatchStats(nodes, nil, nil); err != nil {
		t.Fatalf("save nodes: %v", err)
	}
	facts := []*domain.Fact{
		// grpc_impl：实现类型 → 服务
		{SourceID: "symbol:go:example.com/m/impl:queryServiceImpl", TargetID: "symbol:go:example.com/m/grpc:svc.QueryService",
			Kind: domain.FactGrpcImpl, Confidence: 1.0},
		// grpc 方法一级调用（line_num 决定步骤顺序）
		{SourceID: "symbol:go:example.com/m/impl:(queryServiceImpl).Query", TargetID: "symbol:go:example.com/m/impl:queryHelper",
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": 26}},
		// R55：小写 proto 名方法的实现（Go 导出名）调用链
		{SourceID: "symbol:go:example.com/m/impl:(checkServiceImpl).SendCode", TargetID: "symbol:go:example.com/m/impl:checkHelper",
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": 16}},
	}
	if _, err := r.SaveBatchStats(nil, facts, nil); err != nil {
		t.Fatalf("save facts: %v", err)
	}
	return New(r), dir
}

// TestProcGrpcMethods：GrpcProcMethods——ImplID + 方法名构造 canonical
// ID 展开方法调用链。
func TestProcGrpcMethods(t *testing.T) {
	acts, _ := seedGrpcProcRepo(t)
	svc := GrpcRouteService{Name: "QueryService", Impl: "queryServiceImpl",
		ImplID: "symbol:go:example.com/m/impl:queryServiceImpl",
		Methods: []GrpcRouteMethod{
			{Name: "Query", Handler: "_QueryService_Query_Handler"},
			{Name: "PagingShops"},
		}}
	ms := acts.GrpcProcMethods(svc)
	if len(ms) != 2 {
		t.Fatalf("方法数 = %d; want 2", len(ms))
	}
	if ms[0].Name != "Query" {
		t.Fatalf("methods[0].Name = %q; want Query", ms[0].Name)
	}
	if ms[0].Chain == nil || len(ms[0].Chain.Steps) == 0 {
		t.Error("Query 应有调用链（ImplID.Method 解析成功）")
	}
	if ms[1].Chain == nil || ms[1].Chain.Miss == "" {
		t.Errorf("PagingShops 未定义方法节点——应返回带 Miss 说明的 chain（非 nil 非崩溃）: %+v", ms[1].Chain)
	}
}

// TestProcGrpcMethodsLowerName：R55——ServiceDesc 方法名小写（proto 定义名
// sendCode），实现是 Go 导出名（SendCode）——handler 提取 Go 方法名展开
// 调用链（go2o 实测 CheckService/ContentService 19 个方法因此无调用链）。
func TestProcGrpcMethodsLowerName(t *testing.T) {
	acts, _ := seedGrpcProcRepo(t)
	svc := GrpcRouteService{Name: "CheckService", Impl: "checkServiceImpl",
		ImplID: "symbol:go:example.com/m/impl:checkServiceImpl",
		Methods: []GrpcRouteMethod{
			{Name: "sendCode", Handler: "_CheckService_SendCode_Handler"},
		}}
	ms := acts.GrpcProcMethods(svc)
	if len(ms) != 1 {
		t.Fatalf("方法数 = %d; want 1", len(ms))
	}
	if ms[0].Name != "SendCode" {
		t.Errorf("Name = %q; want SendCode（handler 提取 Go 导出名）", ms[0].Name)
	}
	if ms[0].Chain == nil || len(ms[0].Chain.Steps) == 0 {
		t.Fatalf("sendCode 应有调用链（Go 方法名 SendCode 解析成功）: %+v", ms[0].Chain)
	}
}

// TestProcGrpcMethodsNoCallees：方法节点存在但无项目内调用（Ping 健康
// 检查形态）——Miss 保留"未调用项目内函数"文案，不被误标无效方法（R55）。
func TestProcGrpcMethodsNoCallees(t *testing.T) {
	acts, _ := seedGrpcProcRepo(t)
	svc := GrpcRouteService{Name: "CheckService", Impl: "checkServiceImpl",
		ImplID: "symbol:go:example.com/m/impl:checkServiceImpl",
		Methods: []GrpcRouteMethod{
			{Name: "ping", Handler: "_CheckService_Ping_Handler"},
		}}
	ms := acts.GrpcProcMethods(svc)
	if len(ms) != 1 {
		t.Fatalf("方法数 = %d; want 1", len(ms))
	}
	if ms[0].Chain == nil || ms[0].Chain.Miss == "" {
		t.Fatalf("Ping 应返回带 Miss 的 chain（节点存在但无调用边）: %+v", ms[0].Chain)
	}
	if strings.Contains(ms[0].Chain.Miss, "无效") {
		t.Errorf("Ping 不是无效方法——Miss 不应含无效文案: %q", ms[0].Chain.Miss)
	}
	if !strings.Contains(ms[0].Chain.Miss, "未调用项目内") {
		t.Errorf("Ping Miss 应为未调用项目内函数文案: %q", ms[0].Chain.Miss)
	}
}

// TestGrpcHandlerGoMethod：handler 名提取 Go 方法名（R55）。
func TestGrpcHandlerGoMethod(t *testing.T) {
	cases := []struct {
		handler, svc, want string
	}{
		{"_OrderService_Forbid_Handler", "OrderService", "Forbid"},
		{"_OrderService_PrepareOrderWithCoupon__Handler", "OrderService", "PrepareOrderWithCoupon_"},
		{"_CheckService_SendCode_Handler", "CheckService", "SendCode"},
		{"_MemberService_GetPagedValueGoodsBySaleLabel__Handler", "MemberService", "GetPagedValueGoodsBySaleLabel_"},
		{"_StatusService_Ping_Handler", "StatusService", "Ping"},
		// 不匹配（非生成代码格式/服务名不符）→ 空
		{"_OrderService_Forbid", "OrderService", ""},
		{"_XxxService_Forbid_Handler", "OrderService", ""},
		{"", "OrderService", ""},
	}
	for _, c := range cases {
		if got := GrpcHandlerGoMethod(c.handler, c.svc); got != c.want {
			t.Errorf("GrpcHandlerGoMethod(%q, %q) = %q; want %q", c.handler, c.svc, got, c.want)
		}
	}
}

// TestGrpcMethodEntryID：(Impl).Method canonical ID 构造（R37/R55）。
func TestGrpcMethodEntryID(t *testing.T) {
	cases := []struct {
		implID, method, want string
	}{
		{"symbol:go:example.com/m/impl:queryServiceImpl", "Query", "symbol:go:example.com/m/impl:(queryServiceImpl).Query"},
		{"symbol:go:example.com/m/impl:checkServiceImpl", "SendCode", "symbol:go:example.com/m/impl:(checkServiceImpl).SendCode"},
		{"nocolon", "X", ""},
	}
	for _, c := range cases {
		if got := GrpcMethodEntryID(c.implID, c.method); got != c.want {
			t.Errorf("GrpcMethodEntryID(%q, %q) = %q; want %q", c.implID, c.method, got, c.want)
		}
	}
}
