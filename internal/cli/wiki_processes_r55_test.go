package cli

// R55 grpc 方法入口 Go 方法名（handler 提取）测试（从 wiki_processes_routes_test.go
// 拆出——行数治理 357>300）：小写 proto 方法名恢复调用链、handler 提取、
// 无调用边方法文案不误标无效。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// TestProcGrpcMethodsLowerName：R55——ServiceDesc 方法名小写（proto 定义名
// sendCode），实现是 Go 导出名（SendCode）——handler 提取 Go 方法名展开
// 调用链（go2o 实测 CheckService/ContentService 19 个方法因此无调用链）。
func TestProcGrpcMethodsLowerName(t *testing.T) {
	dir := seedRoutesProcRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	acts := action.New(sqlite.NewRepo(db))
	svc := grpcRouteService{Name: "CheckService", Impl: "checkServiceImpl",
		ImplID: "symbol:go:example.com/m/impl:checkServiceImpl",
		Methods: []grpcRouteMethod{
			{Name: "sendCode", Handler: "_CheckService_SendCode_Handler"},
		}}
	ms := grpcProcMethods(acts, svc)
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
	dir := seedRoutesProcRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	acts := action.New(sqlite.NewRepo(db))
	svc := grpcRouteService{Name: "CheckService", Impl: "checkServiceImpl",
		ImplID: "symbol:go:example.com/m/impl:checkServiceImpl",
		Methods: []grpcRouteMethod{
			{Name: "ping", Handler: "_CheckService_Ping_Handler"},
		}}
	// seed 里无 (checkServiceImpl).Ping 节点——需要补上（无调用边）
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m/impl:(checkServiceImpl).Ping", Kind: domain.KindMethod,
			Name: "(checkServiceImpl).Ping", FilePath: "impl/check.go", LineStart: 30},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	ms := grpcProcMethods(acts, svc)
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
		if got := grpcHandlerGoMethod(c.handler, c.svc); got != c.want {
			t.Errorf("grpcHandlerGoMethod(%q, %q) = %q; want %q", c.handler, c.svc, got, c.want)
		}
	}
}
