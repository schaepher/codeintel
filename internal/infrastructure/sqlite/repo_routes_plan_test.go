package sqlite

// todo22 §7.2：路由投影必须走**可命中的索引**（kind 常量查询 / 属性表达式
// 索引），且不靠过滤合法服务名缩小结果。这里用真实 EXPLAIN QUERY PLAN
// 锁住计划，SQL 与生产共用同一常量（repo_routes.go）。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

func TestRouteProjectionPlanUsesIndex(t *testing.T) {
	r := newTestRepo(t)
	save(t, r, []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:Svc", Kind: domain.KindGrpcService, Name: "Svc",
			FilePath: "svc.go", LineStart: 1,
			Properties: map[string]any{"service_name": "Svc"}},
		{ID: "symbol:go:example.com/m:RegisterSvcServer", Kind: domain.KindFunction,
			Name: "RegisterSvcServer", FilePath: "svc.pb.go", LineStart: 2,
			Properties: map[string]any{"registers_service": "Svc"}},
		{ID: "symbol:go:example.com/m:route", Kind: domain.KindHTTPRoute, Name: "GET /x",
			FilePath: "route.go", LineStart: 3},
	}, nil)

	cases := []struct {
		name  string
		sql   string
		args  []any
		index string
	}{
		{"grpc_service 清单", grpcServicesSQL, nil, "idx_nodes_grpc_service"},
		{"registers_service 注册函数", registerNodeSQL, []any{"Svc"}, "idx_nodes_registers_service"},
	}
	for _, c := range cases {
		rows, err := r.Query("EXPLAIN QUERY PLAN "+c.sql, c.args...)
		if err != nil {
			t.Fatalf("%s: EXPLAIN: %v", c.name, err)
		}
		var plans []string
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				rows.Close()
				t.Fatalf("%s: scan plan: %v", c.name, err)
			}
			plans = append(plans, detail)
		}
		rows.Close()
		joined := strings.Join(plans, " | ")
		if !strings.Contains(joined, c.index) {
			t.Errorf("%s 计划未命中索引 %s:\n%s", c.name, c.index, joined)
		}
		if strings.Contains(joined, "SCAN nodes") && !strings.Contains(joined, "USING") {
			t.Errorf("%s 仍是全表扫描:\n%s", c.name, joined)
		}
	}

	// 结果等价：索引只影响计划，不影响投影内容（不靠过滤服务名缩小结果）
	svcs, err := r.GetGrpcServices()
	if err != nil || len(svcs) != 1 {
		t.Fatalf("GetGrpcServices = %d, %v；期望 1 条", len(svcs), err)
	}
	reg, err := r.GetRegisterNode("Svc")
	if err != nil || reg == nil || reg.Name != "RegisterSvcServer" {
		t.Fatalf("GetRegisterNode = %+v, %v", reg, err)
	}
}
