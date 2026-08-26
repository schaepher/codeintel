package action

// R9x 测试（迁自 cli/query_wiki_common.go wikiRelations 的兜底编排
// 逻辑）：ERRelations——已算直接返回；未算同步兜底计算（渲染断言
// 留在 cli query_wiki_test.go TestQueryERCmd/TestQueryERDefault）。

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// withBuildMeta 写 build_metadata——relation_progress 按 build_id 记录，
// 无 build 时进度恒为未知（GetAllTableRelations 视为未算）。
func withBuildMeta(t *testing.T, dir string) {
	t.Helper()
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	if err := r.Save(&domain.BuildMeta{BuildID: "b1", ToolName: "all", Status: domain.BuildSuccess}); err != nil {
		t.Fatal(err)
	}
}

// TestERRelationsReady：关系已算（Precompute 先跑过）→ 直接返回不报错。
func TestERRelationsReady(t *testing.T) {
	a, dir := seedRepo(t)
	withBuildMeta(t, dir)
	if err := a.PrecomputeAllRelations(nil); err != nil {
		t.Fatal(err)
	}
	rels, err := a.ERRelations()
	if err != nil {
		t.Fatal(err)
	}
	if rels == nil {
		t.Error("rels 应非 nil（可空）")
	}
}

// TestERRelationsComputesOnDemand：未算（进度非 done）→ 内部同步兜底
// 计算并返回；计算后进度置 done（原 cli wikiRelations 行为）。
func TestERRelationsComputesOnDemand(t *testing.T) {
	a, dir := seedRepo(t)
	withBuildMeta(t, dir)
	rels, err := a.ERRelations()
	if err != nil {
		t.Fatalf("未算时应内部兜底计算: %v", err)
	}
	if rels == nil {
		t.Error("rels 应非 nil（可空）")
	}
	p, err := a.RelationProgress()
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != "done" {
		t.Errorf("兜底计算后进度应为 done, got %q", p.Status)
	}
}
