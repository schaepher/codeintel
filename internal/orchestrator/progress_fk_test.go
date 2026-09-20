package orchestrator

import (
	"context"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
	"golang.org/x/tools/go/packages"
)

// danglingAdapter 产出：一个合法节点 + 一条合法边 + 两条悬挂边（端点不存在）。
// Q254 Stage 1：构建期关外键 → 悬挂边能插进去，末尾由 DropDanglingEdges 清掉
// 并计入 skipped（语义与之前"外键报错跳过"一致：悬挂边不进图）。
type danglingAdapter struct{}

func (danglingAdapter) Name() string { return "dangling" }

func (danglingAdapter) Index(_ context.Context, _ *domain.Repository, _ []*packages.Package, emit domain.EmitFunc) error {
	real := domain.CanonicalID("symbol:go:example.com/m:real")
	ghost := domain.CanonicalID("symbol:go:example.com/m:ghost")
	_ = emit(domain.Item{Node: &domain.CodeEntity{
		ID: real, Kind: domain.KindFunction, Name: "real", FilePath: "main.go"}})
	_ = emit(domain.Item{Node: &domain.CodeEntity{
		ID: domain.CanonicalID("symbol:go:example.com/m:other"), Kind: domain.KindFunction,
		Name: "other", FilePath: "main.go"}})
	for _, f := range []*domain.Fact{
		{SourceID: real, TargetID: domain.CanonicalID("symbol:go:example.com/m:other"), Kind: domain.FactCalls},
		{SourceID: real, TargetID: ghost, Kind: domain.FactCalls},
		{SourceID: ghost, TargetID: real, Kind: domain.FactCalls},
	} {
		_ = emit(domain.Item{Fact: f})
	}
	return nil
}

// Q254：全量构建后不得有悬挂边，且计数进 SkippedEdges；外键恢复为开启。
func TestFullBuildDropsDanglingEdges(t *testing.T) {
	o, repo := newTestOrchestrator(t, []domain.IndexerPort{danglingAdapter{}})
	res, err := o.FullBuild(context.Background())
	if err != nil {
		t.Fatalf("FullBuild: %v", err)
	}
	if res.SkippedEdges < 2 {
		t.Errorf("悬挂边应计入 skipped：%+v", res.SkippedEdges)
	}
	n, err := repo.CheckNoDanglingEdges()
	if err != nil {
		t.Fatalf("CheckNoDanglingEdges: %v", err)
	}
	if n != 0 {
		t.Errorf("构建后仍有 %d 条悬挂边", n)
	}
	_, edges, err := repo.Counts()
	if err != nil {
		t.Fatal(err)
	}
	if edges != 1 {
		t.Errorf("合法边应保留 1 条，实际 %d", edges)
	}
	if foreignKeysDisabled(t, repo) {
		t.Error("构建结束后必须恢复 foreign_keys=1（增量路径依赖级联）")
	}
}

// foreignKeysDisabled 读 pragma 确认构建期临时关闭的外键已恢复。
func foreignKeysDisabled(t *testing.T, repo *sqlite.Repo) bool {
	t.Helper()
	var n int
	if err := repo.DB.QueryRow("PRAGMA foreign_keys").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 0
}
