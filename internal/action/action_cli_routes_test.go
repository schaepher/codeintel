package action

// R100 待办13：迁移收尾——cli-routes/grpc-composites/precompute 从 cli
// 直连 sqlite 迁 action（裸 SQL 收口到仓储层窄方法；action 返回结构化
// 结果，cli 只渲染）。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedCLIRoutesRepo 构造 cli_command 节点（顶层 + 嵌套）。
func seedCLIRoutesRepo(t *testing.T) *Actions {
	t.Helper()
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m/cmd:cmd.serve", Kind: domain.KindCLICommand, Name: "serve",
			Properties: map[string]any{"cli_name": "serve", "cli_usage": "启动服务", "cli_action": "serveFn", "cli_parent": "", "register": "cmd/main.go:20"}},
		{ID: "symbol:go:example.com/m/cmd:cmd.db", Kind: domain.KindCLICommand, Name: "db",
			Properties: map[string]any{"cli_name": "db", "cli_usage": "数据库操作", "cli_parent": ""}},
		{ID: "symbol:go:example.com/m/cmd:cmd.db.list", Kind: domain.KindCLICommand, Name: "db.list",
			Properties: map[string]any{"cli_name": "list", "cli_usage": "列表", "cli_action": "listFn", "cli_parent": "db"}},
	}
	if _, err := r.SaveBatchStats(nodes, nil, nil); err != nil {
		t.Fatalf("save nodes: %v", err)
	}
	return New(r)
}

// TestActionsCliRoutes：命令树——顶层命令 + 嵌套子命令组织（裸 SQL 已
// 收口 GetCLICommandNodes）。
func TestActionsCliRoutes(t *testing.T) {
	acts := seedCLIRoutesRepo(t)
	res, err := acts.CliRoutes()
	if err != nil {
		t.Fatalf("CliRoutes: %v", err)
	}
	if len(res.Commands) != 2 {
		t.Fatalf("顶层命令数 = %d; want 2（serve/db）:\n%+v", len(res.Commands), res.Commands)
	}
	var dbCmd *CliCommandEntry
	for i := range res.Commands {
		if res.Commands[i].Name == "db" {
			dbCmd = &res.Commands[i]
		}
	}
	if dbCmd == nil || len(dbCmd.Subcommands) != 1 || dbCmd.Subcommands[0].Name != "list" {
		t.Errorf("db 子命令 = %+v; want [list]", dbCmd)
	}
	if dbCmd.Subcommands[0].Action != "listFn" {
		t.Errorf("db.list action = %q; want listFn", dbCmd.Subcommands[0].Action)
	}
	if dbCmd.Subcommands[0].Register != "" {
		t.Errorf("db.list register = %q; want 空（未配置）", dbCmd.Subcommands[0].Register)
	}
}

// seedGrpcCompositesRepo：组合接口 + 非组合接口。
func seedGrpcCompositesRepo(t *testing.T) *Actions {
	t.Helper()
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m/app:AllService", Kind: domain.KindInterface,
			Name: "AllService", FilePath: "app/all.go", LineStart: 10,
			Properties: map[string]any{"pb_servers": "example.com/m/api.PingServer,example.com/m/api.EchoServer"}},
		{ID: "symbol:go:example.com/m/app:PartialService", Kind: domain.KindInterface,
			Name: "PartialService", FilePath: "app/all.go", LineStart: 20},
	}
	if _, err := r.SaveBatchStats(nodes, nil, nil); err != nil {
		t.Fatalf("save nodes: %v", err)
	}
	return New(r)
}

// TestActionsGrpcComposites：组合接口列出（servers 完整）+ 非组合不出现。
func TestActionsGrpcComposites(t *testing.T) {
	acts := seedGrpcCompositesRepo(t)
	res, err := acts.GrpcComposites()
	if err != nil {
		t.Fatalf("GrpcComposites: %v", err)
	}
	if len(res.Composites) != 1 {
		t.Fatalf("组合接口数 = %d; want 1（非组合不应列出）:\n%+v", len(res.Composites), res.Composites)
	}
	c := res.Composites[0]
	if !strings.Contains(c.Iface, "AllService") {
		t.Errorf("iface = %q; want AllService", c.Iface)
	}
	if len(c.Servers) != 2 || c.Servers[0] != "example.com/m/api.PingServer" || c.Servers[1] != "example.com/m/api.EchoServer" {
		t.Errorf("servers = %v; want PingServer,EchoServer", c.Servers)
	}
	if !strings.Contains(c.Loc, "app/all.go:10") {
		t.Errorf("loc = %q; want app/all.go:10", c.Loc)
	}
}

// seedPrecomputeRepo 构造小型关系图（table_a.id.read → table_b.a_id.
// filter——query 键关联，同 sqlite progressFixture 形态）。
func seedPrecomputeRepo(t *testing.T) (*Actions, string) {
	t.Helper()
	a, dir := seedRepo(t)
	withBuildMeta(t, dir)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	funcID := "symbol:go:example.com/m:find"
	nodes := []*domain.CodeEntity{
		{ID: domain.CanonicalID(funcID), Kind: domain.KindFunction, Name: "find"},
		{ID: domain.CanonicalID(funcID + "#ext.sql.table_a.id.read@6"), Kind: domain.KindFieldAccess,
			Name: "table_a.id", FilePath: "a.go", LineStart: 6,
			Properties: map[string]any{"full_path": "table_a.id", "access_kind": "read",
				"type_string": "sql", "is_external": "true", "func_id": funcID}},
		{ID: domain.CanonicalID(funcID + "#ext.sql.table_b.a_id.filter@9"), Kind: domain.KindFieldAccess,
			Name: "table_b.a_id", FilePath: "a.go", LineStart: 9,
			Properties: map[string]any{"full_path": "table_b.a_id", "access_kind": "filter",
				"type_string": "sql", "is_external": "true", "func_id": funcID}},
		{ID: domain.CanonicalID(funcID + "#t4"), Kind: domain.KindSSAValue, Name: "t4",
			Properties: map[string]any{"func_id": funcID}},
		{ID: domain.CanonicalID(funcID + "#x"), Kind: domain.KindSSAValue, Name: "x",
			Properties: map[string]any{"func_id": funcID}},
	}
	edges := []*domain.Fact{
		{SourceID: domain.CanonicalID(funcID + "#ext.sql.table_a.id.read@6"), TargetID: domain.CanonicalID(funcID + "#t4"),
			Kind: domain.FactSummaryIO, ToolSource: domain.ToolSSA, Confidence: 1},
		{SourceID: domain.CanonicalID(funcID + "#t4"), TargetID: domain.CanonicalID(funcID + "#x"),
			Kind: domain.FactDataFlowsTo, ToolSource: domain.ToolSSA, Confidence: 1},
		{SourceID: domain.CanonicalID(funcID + "#x"), TargetID: domain.CanonicalID(funcID + "#ext.sql.table_b.a_id.filter@9"),
			Kind: domain.FactSummaryIO, ToolSource: domain.ToolSSA, Confidence: 1},
	}
	if _, err := r.SaveBatchStats(nodes, edges, nil); err != nil {
		t.Fatalf("save: %v", err)
	}
	return a, dir
}

// TestActionsPrecomputeRelations：编排——未完成 → 计算 → done + 摘要
// 数据（进度回调触发）；已完成再调 → already-done（不重复计算）。
func TestActionsPrecomputeRelations(t *testing.T) {
	a, _ := seedPrecomputeRepo(t)
	called := false
	res, err := a.PrecomputeRelations(func(done, total int) {
		if total > 0 {
			called = true
		}
	})
	if err != nil {
		t.Fatalf("PrecomputeRelations: %v", err)
	}
	if res.Status != "done" {
		t.Fatalf("status = %q; want done", res.Status)
	}
	if !called {
		t.Error("进度回调应触发")
	}
	if len(res.Rels) == 0 {
		t.Error("done 时应带全量关联（cli 摘要统计用）")
	}
	// 已完成再调 → already-done
	res2, err := a.PrecomputeRelations(nil)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Status != "already-done" {
		t.Errorf("二次调用 status = %q; want already-done", res2.Status)
	}
}
