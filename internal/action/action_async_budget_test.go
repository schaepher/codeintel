package action

import (
	"fmt"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// countingAsyncReader 统计 query async 的逐函数查询次数（todo25 §12）。
type countingAsyncReader struct {
	Reader
	fieldCalls int
	symCalls   int
}

func (r *countingAsyncReader) GetFunctionFields(id domain.CanonicalID) ([]*domain.FunctionFieldSummary, error) {
	r.fieldCalls++
	return r.Reader.GetFunctionFields(id)
}

func (r *countingAsyncReader) GetSymbol(id domain.CanonicalID) (*domain.CodeEntity, error) {
	r.symCalls++
	return r.Reader.GetSymbol(id)
}

func TestAsyncCandidateBudgetLimitsSummaryQueries(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := sqlite.NewRepo(db)

	seedID := domain.CanonicalID("symbol:go:example.com/b:Publish")
	consumeID := domain.CanonicalID("symbol:go:example.com/b:ZConsume")
	callerID := domain.CanonicalID("symbol:go:example.com/b:ZCaller")
	nodes := []*domain.CodeEntity{
		{ID: seedID, Kind: domain.KindFunction, Name: "Publish", FilePath: "b.go", LineStart: 3},
		{ID: consumeID, Kind: domain.KindFunction, Name: "ZConsume", FilePath: "b.go", LineStart: 4},
		{ID: callerID, Kind: domain.KindFunction, Name: "ZCaller", FilePath: "b.go", LineStart: 5},
	}
	var sums []*domain.FunctionFieldSummary
	sums = append(sums, &domain.FunctionFieldSummary{FunctionID: seedID,
		AccessKind: domain.SummaryDirectWrite, FieldPath: "example.com/b.Flow.Id", LineStart: 3})
	sums = append(sums, &domain.FunctionFieldSummary{FunctionID: consumeID,
		AccessKind: domain.SummaryDirectRead, FieldPath: "example.com/b.Flow.Id", LineStart: 4})
	for i := 0; i < 220; i++ {
		id := domain.CanonicalID(fmt.Sprintf("symbol:go:example.com/b:A%03d", i))
		nodes = append(nodes, &domain.CodeEntity{ID: id, Kind: domain.KindFunction,
			Name: fmt.Sprintf("A%03d", i), FilePath: "b.go", LineStart: 10 + i})
		sums = append(sums, &domain.FunctionFieldSummary{FunctionID: id,
			AccessKind: domain.SummaryDirectRead,
			FieldPath:  fmt.Sprintf("example.com/b.Cache%03d.Id", i), LineStart: 10 + i})
	}
	facts := []*domain.Fact{{SourceID: callerID, TargetID: seedID, Kind: domain.FactCalls,
		ToolSource: domain.ToolCodeGraph, Confidence: 0.9}}
	if _, err := repo.SaveBatchStats(nodes, facts, sums); err != nil {
		t.Fatalf("save: %v", err)
	}
	cr := &countingAsyncReader{Reader: repo}
	res, err := New(cr).AsyncLifecycle(AsyncLifecycleRequest{Seed: string(seedID), RepoAbs: dir})
	if err != nil {
		t.Fatal(err)
	}
	if res.CandidateTotal < 220 || !res.Partial || len(res.Nodes) != 201 {
		t.Errorf("预算/节点数不符: total=%d partial=%v nodes=%d",
			res.CandidateTotal, res.Partial, len(res.Nodes))
	}
	if cr.fieldCalls > 201 {
		t.Errorf("GetFunctionFields 调用 %d 次；预算内应 ≤201", cr.fieldCalls)
	}
	if cr.symCalls > 404 {
		t.Errorf("GetSymbol 调用 %d 次；应 ≤404", cr.symCalls)
	}
	inNodes := map[string]bool{}
	for _, n := range res.Nodes {
		inNodes[n.ID] = true
	}
	for _, n := range res.Nodes {
		if n.ID != string(seedID) && n.CandidateReason == "" {
			t.Errorf("候选缺 candidate_reason: %+v", n)
		}
	}
	_ = inNodes
}
