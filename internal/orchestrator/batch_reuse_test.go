package orchestrator

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestBatchReuseResetsState（todo9-7）：归还后的批次对象切片清空、
// 去重表清空（同一节点再出现必须重新写入），容量保留。
func TestBatchReuseResetsState(t *testing.T) {
	b := getBatch()
	n := &domain.CodeEntity{ID: "symbol:go:x:F", Kind: domain.KindFunction, Name: "F"}
	b.nodes = append(b.nodes, n)
	b.edges = append(b.edges, &domain.Fact{SourceID: "a", TargetID: "b", Kind: domain.FactCalls})
	b.summaries = append(b.summaries, &domain.FunctionFieldSummary{FunctionID: "f"})
	b.origins = append(b.origins, &domain.SummaryOrigin{FunctionID: "f"})
	if !b.dedup.shouldAppend(n) {
		t.Fatal("首次应写入")
	}
	if b.dedup.shouldAppend(n) {
		t.Fatal("同节点第二次应被去重")
	}
	putBatch(b)

	b2 := getBatch()
	if len(b2.nodes)+len(b2.edges)+len(b2.summaries)+len(b2.origins) != 0 {
		t.Fatalf("复用的批次未清空: nodes=%d edges=%d summaries=%d origins=%d",
			len(b2.nodes), len(b2.edges), len(b2.summaries), len(b2.origins))
	}
	if !b2.dedup.shouldAppend(n) {
		t.Fatal("去重表未清空（新批次应重新写入同一节点）")
	}
	putBatch(b2)
}

// TestNodeDedupReset：clear 后等价于新表（保留 A→B→A 语义不受影响）。
func TestNodeDedupReset(t *testing.T) {
	d := newNodeDedup()
	a := &domain.CodeEntity{ID: "a", Kind: domain.KindFunction, Name: "A"}
	b := &domain.CodeEntity{ID: "a", Kind: domain.KindFunction, Name: "B"}
	if !d.shouldAppend(a) || d.shouldAppend(a) {
		t.Fatal("A 的首次/重复判定错误")
	}
	if !d.shouldAppend(b) {
		t.Fatal("内容不同的同 ID 节点应写入")
	}
	d.reset()
	if !d.shouldAppend(a) {
		t.Fatal("reset 后应重新写入")
	}
}
