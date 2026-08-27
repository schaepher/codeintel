package cli

// R35 query cli-routes 测试：cli_command 节点 → 命令树 JSON（含嵌套
// 子命令）。测试先行。

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedCLIRoutesRepo 构造 cli_command 节点（顶层 + 嵌套）。
func seedCLIRoutesRepo(t *testing.T) string {
	t.Helper()
	dir := seedRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
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
	return dir
}

// TestQueryCLIRoutes：命令树 JSON——顶层命令 + 嵌套子命令组织（R100
// 查询逻辑迁 action——cli 渲染层测试）。
func TestQueryCLIRoutes(t *testing.T) {
	dir := seedCLIRoutesRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	res, err := action.New(sqlite.NewRepo(db)).CliRoutes()
	if err != nil {
		t.Fatalf("CliRoutes: %v", err)
	}
	if len(res.Commands) != 2 {
		t.Fatalf("顶层命令数 = %d; want 2（serve/db）:\n%+v", len(res.Commands), res.Commands)
	}
	// serve 顶层
	if res.Commands[0].Name != "db" && res.Commands[1].Name != "db" {
		t.Errorf("缺 db 命令: %+v", res.Commands)
	}
	var dbCmd *action.CliCommandEntry
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
	// JSON 契约
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"commands"`, `"subcommands"`, `"name"`, `"usage"`, `"action"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("JSON 缺 %q:\n%s", want, b)
		}
	}
}
