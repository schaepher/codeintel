package cli

// P1-05：仓库外表 schema 事实源（全局注册表 ~/.codeintel/codeintel.db）。
//
// R19/R21 的四层类型兜底（yaml > 仓库 sqlite_master > gorm tag > Go 类型）
// 对被分析仓库之外的表无效——本仓库 wiki 的 `repos` 表（全局注册台账）
// 有 10 列无类型。注册表自身就是 SQLite 库，其 sqlite_master 是同样
// 零 AI 的权威事实源。

import (
	"testing"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// TestMergeSchemaDDLs 合并语义：仓库内 DDL 优先，仓库外只补缺失表。
func TestMergeSchemaDDLs(t *testing.T) {
	base := map[string]string{"repos": "CREATE TABLE repos (x TEXT)"}
	extra := map[string]string{
		"repos": "CREATE TABLE repos (x INTEGER)",
		"other": "CREATE TABLE other (y TEXT)",
	}
	got := mergeSchemaDDLs(base, extra)
	if got["repos"] != base["repos"] {
		t.Errorf("同名表应由仓库内 DDL 优先，得到 %q", got["repos"])
	}
	if got["other"] != extra["other"] {
		t.Errorf("仓库外的表应补入，得到 %q", got["other"])
	}
	// 不改入参
	if len(base) != 1 || len(extra) != 2 {
		t.Errorf("不应修改入参：base=%v extra=%v", base, extra)
	}
	// nil base 不 panic
	if got := mergeSchemaDDLs(nil, map[string]string{"a": "ddl"}); got["a"] != "ddl" {
		t.Errorf("nil base 应可合并，得到 %v", got)
	}
}

// TestWikiSchemasMergesGlobalRegistry wikiSchemas 合并全局注册表 schema：
// repos 表列类型可填充，同时仓库自身表仍在（不被覆盖）。
func TestWikiSchemasMergesGlobalRegistry(t *testing.T) {
	regDir := t.TempDir()
	reg, err := sqlite.OpenRegistry(regDir)
	if err != nil {
		t.Fatalf("OpenRegistry: %v", err)
	}
	reg.Close()
	old := registryDirFn
	registryDirFn = func() string { return regDir }
	t.Cleanup(func() { registryDirFn = old })

	dir := seedRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	schemas := wikiSchemas(action.New(sqlite.NewRepo(db)))

	repos := schemas["repos"]
	if repos == nil {
		t.Fatal("应从全局注册表补出 repos 表 schema")
	}
	if c := repos["path"]; c.Typ != "TEXT" {
		t.Errorf("repos.path = %q, want TEXT", c.Typ)
	}
	if c := repos["go_mod_count"]; c.Typ != "INTEGER" {
		t.Errorf("repos.go_mod_count = %q, want INTEGER", c.Typ)
	}
	if c := repos["registered_at"]; c.Typ != "TEXT" {
		t.Errorf("repos.registered_at = %q, want TEXT", c.Typ)
	}
	// 仓库自身的表（nodes 等）不应因合并丢失
	if schemas["nodes"] == nil {
		t.Error("仓库 sqlite_master 的表应保留")
	}
}
