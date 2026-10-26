package action

// todo14-17：value-trace 真实阶段计数、有界缓存与逐阶段取消。

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

func seedTraceStatsRepo(t *testing.T) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package m\n\nfunc Fn() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:(T).Fn", Kind: domain.KindMethod, Name: "(T).Fn",
			FilePath: "main.go", LineStart: 3},
	}
	for _, acc := range []string{"write", "read"} {
		nodes = append(nodes, &domain.CodeEntity{
			ID: domain.CanonicalID("symbol:go:example.com/m:(T).Fn#h.State." + acc + "@3"),
			Kind: domain.KindFieldAccess, Name: "h.State", FilePath: "main.go", LineStart: 3,
			Properties: map[string]any{"access_kind": acc, "full_path": "example.com/m.T.State",
				"func_id": "symbol:go:example.com/m:(T).Fn", "instance_path": "h.State"}})
	}
	if _, err := r.SaveBatchStats(nodes, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "vt-stats", ToolName: "test", Status: domain.BuildSuccess}); err != nil {
		t.Fatal(err)
	}
	return New(r), dir
}

func TestTraceStatsActualCounts(t *testing.T) {
	acts, _ := seedTraceStatsRepo(t)
	id := domain.CanonicalID("symbol:go:example.com/m:(T).Fn#h.State.write@3")
	rows, st, err := acts.ValueTraceWithContext(context.Background(), id, 8, 1.0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatalf("应有行")
	}
	if st.SQLQueries < 2 || st.SymbolLookups != 1 || st.ConditionRows != 1 || st.DispatchCalls != 1 {
		t.Errorf("阶段计数应为实际发生次数: %+v", st)
	}
	if len(st.CoveredOps) == 0 || len(st.UncoveredOps) == 0 {
		t.Errorf("应列覆盖/未覆盖口径: %+v", st)
	}
}

func TestTraceCacheBoundedAndInvalidated(t *testing.T) {
	acts, dir := seedTraceStatsRepo(t)
	acts.SetValueTraceCacheLimit(1)
	ids := []domain.CanonicalID{
		"symbol:go:example.com/m:(T).Fn#h.State.write@3",
		"symbol:go:example.com/m:(T).Fn#h.State.read@3",
		"symbol:go:example.com/m:(T).Fn",
	}
	for _, id := range ids {
		if _, _, err := acts.ValueTraceWithContext(context.Background(), id, 8, 1.0, false); err != nil {
			t.Fatal(err)
		}
	}
	_, st, err := acts.ValueTraceWithContext(context.Background(), ids[2], 8, 1.0, false)
	if err != nil {
		t.Fatal(err)
	}
	if st.CacheEntries > 1 || st.CacheEvictions == 0 {
		t.Errorf("同索引缓存应有容量上限与淘汰: %+v", st)
	}
	// 重建后旧缓存不命中
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlite.NewRepo(db).Save(&domain.BuildMeta{BuildID: "vt-stats-2",
		ToolName: "test", Status: domain.BuildSuccess}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	_, st2, err := acts.ValueTraceWithContext(context.Background(), ids[2], 8, 1.0, false)
	if err != nil {
		t.Fatal(err)
	}
	if st2.CacheMisses != 1 || st2.CacheHits != 0 {
		t.Errorf("重建后旧缓存不得命中: %+v", st2)
	}
}

func TestTraceCancelEachStage(t *testing.T) {
	acts, _ := seedTraceStatsRepo(t)
	id := domain.CanonicalID("symbol:go:example.com/m:(T).Fn#h.State.write@3")
	defer func() { traceStageHook = nil }()
	for i, phase := range []string{"after_sql", "after_conditions", "after_dispatch"} {
		ctx, cancel := context.WithCancel(context.Background())
		traceStageHook = func(p string) {
			if p == phase {
				cancel()
			}
		}
		_, _, err := acts.ValueTraceWithContext(ctx, id, 5+i, 1.0, false)
		cancel()
		if !errors.Is(err, ErrTraceCanceled) {
			t.Errorf("阶段 %s 预算耗尽应取消（不得 complete）: err=%v", phase, err)
		}
	}
}
