package sqlite

// R69 edge count 测试：同义边 UPSERT 累加 count（真实调用次数——
// UNIQUE(source,target,kind) 合并不再丢次数）；高置信度保留。

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestEdgeCountUpsert：同 (source,target,kind) 多次插入 → count 累加。
func TestEdgeCountUpsert(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := NewRepo(db)
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:m:a", Kind: domain.KindFunction, Name: "a"},
		{ID: "symbol:go:m:b", Kind: domain.KindFunction, Name: "b"},
	}
	if _, err := r.SaveBatchStats(nodes, nil, nil); err != nil {
		t.Fatal(err)
	}
	fact := &domain.Fact{SourceID: "symbol:go:m:a", TargetID: "symbol:go:m:b",
		Kind: domain.FactCalls, Confidence: 0.8}
	// 3 次调用（同义边合并）
	for i := 0; i < 3; i++ {
		if _, err := r.SaveBatchStats(nil, []*domain.Fact{fact}, nil); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT count FROM edges WHERE source_id = ? AND target_id = ? AND kind = 'calls'`,
		"symbol:go:m:a", "symbol:go:m:b").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Errorf("count = %d; want 3（同义边累加调用次数）", count)
	}
	// 更高置信度 → 覆盖 confidence 且继续累加
	if _, err := r.SaveBatchStats(nil, []*domain.Fact{{
		SourceID: "symbol:go:m:a", TargetID: "symbol:go:m:b",
		Kind: domain.FactCalls, Confidence: 0.95}}, nil); err != nil {
		t.Fatal(err)
	}
	var count2 float64
	var conf float64
	if err := db.QueryRow(`SELECT count, confidence FROM edges WHERE source_id = ? AND target_id = ? AND kind = 'calls'`,
		"symbol:go:m:a", "symbol:go:m:b").Scan(&count2, &conf); err != nil {
		t.Fatal(err)
	}
	if count2 != 4 {
		t.Errorf("count = %v; want 4（高置信度插入也累加）", count2)
	}
	if conf != 0.95 {
		t.Errorf("confidence = %v; want 0.95（高置信度保留）", conf)
	}
}

// TestEdgeCountRead：queryEdges 带 count（实体聚合数据源）。
func TestEdgeCountRead(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:m:a", Kind: domain.KindFunction, Name: "a"},
		{ID: "symbol:go:m:b", Kind: domain.KindFunction, Name: "b"},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := r.SaveBatchStats(nil, []*domain.Fact{{
			SourceID: "symbol:go:m:a", TargetID: "symbol:go:m:b",
			Kind: domain.FactCalls, Confidence: 0.9}}, nil); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := r.GetEntityRaw()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range raw.Calls {
		if string(c.SourceID) == "symbol:go:m:a" && c.Count != 5 {
			t.Errorf("calls Fact.Count = %d; want 5（queryEdges 带 count）", c.Count)
		}
	}
}
