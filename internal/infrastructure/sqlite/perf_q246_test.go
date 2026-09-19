package sqlite

import (
	"fmt"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// Q246 写库侧基准：同一次构建里 nodes/edges 的插入吞吐（schema 索引 +
// DSN pragma 改动的数字验收）。用生产的 Open（同 DSN）与生产 DDL，
// 每轮迭代建新库、插入固定规模、计时。
//
// 跑法：go test ./internal/infrastructure/sqlite/ -run XXX -bench SaveBatchInsert -count=3
// 对照：在 HEAD 的 worktree 里跑同一文件（同机顺序执行，避开机器噪声）。
//
// 注意：本机内存小（Q246 实测 3GB），DB 会被 page cache 影响——每轮
// 迭代新库 + 固定规模保证可比；绝对值不代表大库（300MB+）的稳态。
func BenchmarkSaveBatchInsert(b *testing.B) {
	const nodesPerIter = 2000
	const edgesPerIter = 6000
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		dir := b.TempDir() // 每轮新库（清空 page cache 之外的干扰）
		db, err := Open(dir)
		if err != nil {
			b.Fatalf("open: %v", err)
		}
		r := NewRepo(db)
		nodes := make([]*domain.CodeEntity, 0, nodesPerIter)
		for j := 0; j < nodesPerIter; j++ {
			kind := domain.KindFunction
			props := map[string]any{"signature": "func f()", "full_path": "t.a"}
			switch j % 4 {
			case 1:
				kind = domain.KindFieldAccess
			case 2:
				kind = domain.KindSSAValue
			case 3:
				props["func_id"] = "symbol:go:m:f"
			}
			nodes = append(nodes, &domain.CodeEntity{
				ID: domain.CanonicalID(fmt.Sprintf("symbol:go:m:f%d", j)), Kind: kind,
				Name: "f", FilePath: "a.go", LineStart: j, LineEnd: j + 1, Properties: props,
			})
		}
		edges := make([]*domain.Fact, 0, edgesPerIter)
		for j := 0; j < edgesPerIter; j++ {
			edges = append(edges, &domain.Fact{
				SourceID: domain.CanonicalID(fmt.Sprintf("symbol:go:m:f%d", j%nodesPerIter)),
				TargetID: domain.CanonicalID(fmt.Sprintf("symbol:go:m:f%d", (j*7)%nodesPerIter)),
				Kind:     domain.FactCalls, ToolSource: domain.ToolSSA, Confidence: 0.8,
			})
		}
		b.StartTimer()
		if _, err := r.SaveBatchStats(nodes, edges, nil); err != nil {
			b.Fatalf("save: %v", err)
		}
		b.StopTimer()
		db.Close()
	}
}
