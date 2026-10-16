package orchestrator

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

const mergeNodeID = "symbol:go:example.com/x:F"

func mergeNode(props map[string]any) *domain.CodeEntity {
	return &domain.CodeEntity{
		ID:         domain.CanonicalID(mergeNodeID),
		Kind:       domain.KindFunction,
		Name:       "F",
		FilePath:   "a.go",
		LineStart:  1,
		Properties: props,
	}
}

func openMergeRepo(t *testing.T) *sqlite.Repo {
	t.Helper()
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return sqlite.NewRepo(db)
}

func propsOf(t *testing.T, r *sqlite.Repo) map[string]any {
	t.Helper()
	n, err := r.GetSymbol(domain.CanonicalID(mergeNodeID))
	if err != nil || n == nil {
		t.Fatalf("GetSymbol: %v / %v", n, err)
	}
	return n.Properties
}

// TestNodeMergeFidelityAcrossBatches：同一节点在多个批次写入不同贡献，
// 最终 properties 必须是 json_patch 合并结果（两批都不丢）。
func TestNodeMergeFidelityAcrossBatches(t *testing.T) {
	r := openMergeRepo(t)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{mergeNode(map[string]any{"a": "1"})}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{mergeNode(map[string]any{"b": "2"})}, nil, nil); err != nil {
		t.Fatal(err)
	}
	p := propsOf(t, r)
	if p["a"] != "1" || p["b"] != "2" {
		t.Errorf("properties = %v, want a=1 b=2（两批贡献都保留）", p)
	}
}

// TestNodeMergeFidelityWithDedup：编排层等价节点去重（node_batch）与 DB
// json_patch 合并语义一致——A→B→A 的不同贡献全部落库；连续相同的第二次
// 被跳过但最终内容不变。
func TestNodeMergeFidelityWithDedup(t *testing.T) {
	r := openMergeRepo(t)
	d := newNodeDedup()
	a1 := mergeNode(map[string]any{"a": "1"})
	b := mergeNode(map[string]any{"b": "2"})
	a2 := mergeNode(map[string]any{"a": "1"})
	var batch []*domain.CodeEntity
	for _, n := range []*domain.CodeEntity{a1, b, a2} {
		if d.shouldAppend(n) {
			batch = append(batch, n)
		}
	}
	if len(batch) != 3 {
		t.Fatalf("A→B→A 应全部保留，got %d", len(batch))
	}
	if _, err := r.SaveBatchStats(batch, nil, nil); err != nil {
		t.Fatal(err)
	}
	p := propsOf(t, r)
	if p["a"] != "1" || p["b"] != "2" {
		t.Errorf("properties = %v, want a=1 b=2", p)
	}

	// 连续相同：第二次跳过，库内容不变（再写一次也不改变合并结果）
	d2 := newNodeDedup()
	var batch2 []*domain.CodeEntity
	x := mergeNode(map[string]any{"a": "1", "b": "2"})
	for i := 0; i < 2; i++ {
		if d2.shouldAppend(x) {
			batch2 = append(batch2, x)
		}
	}
	if len(batch2) != 1 {
		t.Fatalf("连续相同应只保留 1 次，got %d", len(batch2))
	}
	if _, err := r.SaveBatchStats(batch2, nil, nil); err != nil {
		t.Fatal(err)
	}
	if p := propsOf(t, r); p["a"] != "1" || p["b"] != "2" {
		t.Errorf("去重后 properties = %v, want a=1 b=2", p)
	}
}
