package action

// R100 待办9（wiki 数据源全部改 action）：wiki 残余直连数据源迁移——
// archSvcPkgs（裸 SQL）、PkgCodeFacts、QAReferences、ORMStructs、
// APIRoutes（源码解析）。wiki 只组合 action 结果到 html/md。

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// TestArchSvcPkgs：grpc_service/http_route 节点 → 包短名集合（裸 SQL
// 收口——组合 GetGrpcServices + GetHTTPRouteNodes）。
func TestArchSvcPkgs(t *testing.T) {
	a, dir := seedRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m/api:svc.QueryService", Kind: domain.KindGrpcService, Name: "svc.QueryService"},
		{ID: "symbol:go:example.com/m/api:route.1", Kind: domain.KindHTTPRoute, Name: "GET /x"},
		{ID: "symbol:go:example.com/m/store:route.2", Kind: domain.KindHTTPRoute, Name: "POST /y"},
	}
	if _, err := r.SaveBatchStats(nodes, nil, nil); err != nil {
		t.Fatal(err)
	}
	pkgs, err := a.ArchSvcPkgs()
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 2 || pkgs[0] != "api" || pkgs[1] != "store" {
		t.Errorf("ArchSvcPkgs = %v; want [api store]（包短名去重排序）", pkgs)
	}
}

// TestORMStructs：源码 TableName() 方法 → 表名 ↔ 结构体（含字段列映射）。
func TestORMStructs(t *testing.T) {
	a, dir := seedRepo(t)
	src := `package model

import "time"

// Order 订单
type Order struct {
	ID    int64     ` + "`gorm:\"primaryKey;autoIncrement\"`" + `
	Name  string    ` + "`gorm:\"column:order_name\"`" + `
	CTime time.Time
}

func (Order) TableName() string { return "order_main" }
`
	if err := os.WriteFile(filepath.Join(dir, "model.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	orm, err := a.ORMStructs(dir)
	if err != nil {
		t.Fatal(err)
	}
	orderMain := orm["order_main"]
	if len(orderMain) != 1 || orderMain[0].Name != "Order" {
		t.Fatalf("order_main 结构体 = %+v; want [Order]", orderMain)
	}
	if len(orderMain[0].Fields) != 3 {
		t.Fatalf("Order 字段 = %d; want 3: %+v", len(orderMain[0].Fields), orderMain[0].Fields)
	}
	if orderMain[0].Fields[0].Name != "ID" || !orderMain[0].Fields[0].IsAutoInc {
		t.Errorf("ID 应识别自增: %+v", orderMain[0].Fields[0])
	}
	if orderMain[0].Fields[1].Column != "order_name" {
		t.Errorf("Name 列 = %q; want order_name（tag 优先）", orderMain[0].Fields[1].Column)
	}
}

// TestAPIRoutes：internal/server 包源码 → 路由清单（handler 注释作描述）。
func TestAPIRoutes(t *testing.T) {
	a, dir := seedRepo(t)
	if err := os.MkdirAll(filepath.Join(dir, "internal", "server"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "server", "server.go"), []byte(`package server

import "net/http"

// 查询订单
func (s *Server) handleOrders(w http.ResponseWriter, r *http.Request) {}

func (s *Server) New() {
	mux.HandleFunc("/orders", s.handleOrders) // 订单列表
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	routes, err := a.APIRoutes(dir)
	if err != nil {
		t.Fatal(err)
	}
	var orders *domain.APIRoute
	for i := range routes {
		if routes[i].Path == "/orders" {
			orders = &routes[i]
		}
	}
	if orders == nil {
		t.Fatalf("缺 /orders 路由: %+v", routes)
	}
	if orders.Handler != "handleOrders" {
		t.Errorf("handler = %q; want handleOrders", orders.Handler)
	}
	if orders.Desc == "" {
		t.Error("Desc 应含 handler 注释（查询订单）")
	}
}
