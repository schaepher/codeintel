package sqlite

// todo21-14.3：一跳直接邻接查询与旧递归 CTE 的兼容矩阵。
//
// depth<=1 改走直接查询（去掉递归规划/执行成本），但输出必须与旧递归
// 语义逐项一致：DISTINCT、六列结果、kind='calls'、置信度过滤、depth<=0
// 仍取一跳、多跳递归不变。反例覆盖：双方向、环、低置信度、非 calls 边、
// metadata、重复事实去重。

import (
	"context"
	"fmt"
	"sort"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// recursiveReference 旧实现（递归 CTE，非物化）——兼容性对照基准。
func recursiveReference(t *testing.T, r *Repo, id string, depth int, minConfidence float64, dir string) []string {
	t.Helper()
	var anchor, walkCol string
	if dir == "callers" {
		anchor, walkCol = "target_id", "src"
	} else {
		anchor, walkCol = "source_id", "tgt"
	}
	q := fmt.Sprintf(`
WITH RECURSIVE walk(src, tgt, kind, tool_source, confidence, metadata, d) AS (
    SELECT source_id, target_id, kind, tool_source, confidence, metadata, 1
    FROM edges_v WHERE %s = ? AND kind = 'calls' AND confidence >= ?
    UNION
    SELECT e.source_id, e.target_id, e.kind, e.tool_source, e.confidence, e.metadata, w.d + 1
    FROM edges_v e JOIN walk w ON e.%s = w.%s
    WHERE w.d < ? AND e.kind = 'calls' AND e.confidence >= ?
)
SELECT DISTINCT src, tgt, kind, tool_source, confidence, metadata FROM walk`,
		anchor, anchor, walkCol)
	rows, err := r.Query(q, id, minConfidence, depth, minConfidence)
	if err != nil {
		t.Fatalf("reference query: %v", err)
	}
	defer rows.Close()
	facts, err := scanFacts(rows)
	if err != nil {
		t.Fatalf("scan reference: %v", err)
	}
	return factSet(facts)
}

// factSet 事实集合的规范化字符串（无序比较；metadata 按 json 文本参与）。
func factSet(facts []*domain.Fact) []string {
	out := make([]string, 0, len(facts))
	for _, f := range facts {
		out = append(out, fmt.Sprintf("%s|%s|%s|%s|%.3f|%v", f.SourceID, f.TargetID,
			f.Kind, f.ToolSource, f.Confidence, f.Metadata))
	}
	sort.Strings(out)
	return out
}

func seedWalkMatrix(t *testing.T, r *Repo) {
	t.Helper()
	ids := []string{"a", "b", "c", "d"}
	nodes := make([]*domain.CodeEntity, 0, len(ids))
	for _, id := range ids {
		nodes = append(nodes, node("symbol:go:example.com/m:"+id, "function", id, id+".go"))
	}
	save(t, r, nodes, nil)
	mk := func(src, tgt string, conf float64, line int) *domain.Fact {
		return &domain.Fact{
			SourceID: domain.CanonicalID("symbol:go:example.com/m:" + src),
			TargetID: domain.CanonicalID("symbol:go:example.com/m:" + tgt),
			Kind:     domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: conf,
			Metadata: map[string]any{"line_num": float64(line)},
		}
	}
	save(t, r, nil, []*domain.Fact{
		mk("a", "b", 0.8, 5),
		mk("b", "c", 0.9, 7),
		mk("b", "a", 1.0, 9),  // 环 b→a
		mk("a", "d", 0.5, 11), // 低置信度
		{SourceID: "symbol:go:example.com/m:a", TargetID: "symbol:go:example.com/m:d",
			Kind: domain.FactImports, ToolSource: domain.ToolCodeGraph, Confidence: 1}, // 非 calls
	})
}

// TestWalkEdgesOneHopMatchesRecursive：depth<=1 的直接查询与旧递归实现
// 输出一致（0/1/1 双方向/带环/低置信度）。
func TestWalkEdgesOneHopMatchesRecursive(t *testing.T) {
	r := newTestRepo(t)
	seedWalkMatrix(t, r)
	anchor := domain.CanonicalID("symbol:go:example.com/m:a")
	for _, depth := range []int{-1, 0, 1} {
		for _, dir := range []string{"callees", "callers"} {
			var id string
			if dir == "callees" {
				id = string(anchor)
			} else {
				id = "symbol:go:example.com/m:c"
			}
			got, err := r.walkEdges(context.Background(), id, depth, 0.8, dir)
			if err != nil {
				t.Fatalf("walkEdges(%s, depth=%d): %v", dir, depth, err)
			}
			want := recursiveReference(t, r, id, depth, 0.8, dir)
			if fmt.Sprint(factSet(got)) != fmt.Sprint(want) {
				t.Errorf("%s depth=%d 不一致:\n got %v\nwant %v", dir, depth, factSet(got), want)
			}
		}
	}
	// 一跳具体结果：a 只调 b（低置信度 a→d 与 imports a→d 必须排除）
	got, _ := r.walkEdges(context.Background(), string(anchor), 1, 0.8, "callees")
	if len(got) != 1 || got[0].TargetID != "symbol:go:example.com/m:b" {
		t.Fatalf("一跳 callees(a) = %+v，应只有 b", got)
	}
	if got[0].Metadata["line_num"] != float64(5) {
		t.Errorf("metadata 丢失: %+v", got[0].Metadata)
	}
	// callers 方向：c 的调用者是 b
	got, _ = r.walkEdges(context.Background(), "symbol:go:example.com/m:c", 1, 0.8, "callers")
	if len(got) != 1 || got[0].SourceID != "symbol:go:example.com/m:b" {
		t.Fatalf("一跳 callers(c) = %+v，应只有 b", got)
	}
}

// TestWalkEdgesMultiHopMatchesRecursive：depth>1 仍走递归（物化）语义，
// 含环上的重复事实去重（a→b 在 d=1 与 d=3 各出现一次，DISTINCT 后一条）。
func TestWalkEdgesMultiHopMatchesRecursive(t *testing.T) {
	r := newTestRepo(t)
	seedWalkMatrix(t, r)
	anchor := "symbol:go:example.com/m:a"
	for _, depth := range []int{2, 3} {
		for _, dir := range []string{"callees", "callers"} {
			id := anchor
			if dir == "callers" {
				id = "symbol:go:example.com/m:c"
			}
			got, err := r.walkEdges(context.Background(), id, depth, 0.8, dir)
			if err != nil {
				t.Fatalf("walkEdges(%s, depth=%d): %v", dir, depth, err)
			}
			want := recursiveReference(t, r, id, depth, 0.8, dir)
			if fmt.Sprint(factSet(got)) != fmt.Sprint(want) {
				t.Errorf("%s depth=%d 不一致:\n got %v\nwant %v", dir, depth, factSet(got), want)
			}
		}
	}
	// 多跳：a→b、b→c、b→a（环）——每条只保留一条（DISTINCT）
	got, err := r.GetCallees(domain.CanonicalID(anchor), 2, 0.8)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("depth2 callees(a) = %d 条（应 3：a→b、b→a、b→c）: %+v", len(got), got)
	}
	seen := map[string]int{}
	for _, f := range got {
		seen[string(f.SourceID)+"->"+string(f.TargetID)]++
	}
	if seen["symbol:go:example.com/m:a->symbol:go:example.com/m:b"] != 1 {
		t.Errorf("重复事实未去重: %v", seen)
	}
}
