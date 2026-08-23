package action

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// Q244 before/trace 意图命令：目标形态分派 + 聚合。

func beforeActs(t *testing.T) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	funcID := domain.CanonicalID("symbol:go:example.com/m:main")
	startID := domain.CanonicalID("symbol:go:example.com/m:start")
	nodes := []*domain.CodeEntity{
		{ID: funcID, Kind: domain.KindFunction, Name: "main", FilePath: "main.go", LineStart: 3},
		{ID: startID, Kind: domain.KindFunction, Name: "start", FilePath: "start.go", LineStart: 1},
		{ID: funcID + "#t.A.write@5", Kind: domain.KindFieldAccess, Name: "t.A", FilePath: "main.go", LineStart: 5,
			Properties: map[string]any{"full_path": "example.com/m.T.A", "access_kind": "write", "func_id": string(funcID)}},
		{ID: funcID + "#t.A.read@7", Kind: domain.KindFieldAccess, Name: "t.A", FilePath: "main.go", LineStart: 7,
			Properties: map[string]any{"full_path": "example.com/m.T.A", "access_kind": "read", "func_id": string(funcID)}},
	}
	if _, err := r.SaveBatchStats(nodes, []*domain.Fact{
		{SourceID: startID, TargetID: funcID, Kind: domain.FactCalls, Confidence: 0.9},
	}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SaveBatchStats(nil, nil, []*domain.FunctionFieldSummary{
		{FunctionID: funcID, AccessKind: domain.SummaryDirectWrite, FieldPath: "example.com/m.T.A", InstancePath: "t.A", LineStart: 5, CodeSnippet: "t.A = v"},
		{FunctionID: funcID, AccessKind: domain.SummaryDirectRead, FieldPath: "example.com/m.T.A", InstancePath: "t.A", LineStart: 7, CodeSnippet: "return t.A"},
	}); err != nil {
		t.Fatal(err)
	}
	return New(r), dir
}

// TestResolveBeforeTargetSymbol：符号名 → kind=symbol。
func TestResolveBeforeTargetSymbol(t *testing.T) {
	a, _ := beforeActs(t)
	tgt, err := a.ResolveBeforeTarget("main")
	if err != nil {
		t.Fatalf("ResolveBeforeTarget: %v", err)
	}
	if tgt.Kind != "symbol" || tgt.Name != "main" {
		t.Errorf("target = %+v, want symbol/main", tgt)
	}
}

// TestResolveBeforeTargetField：字段路径（含 .）→ kind=field。
func TestResolveBeforeTargetField(t *testing.T) {
	a, _ := beforeActs(t)
	tgt, err := a.ResolveBeforeTarget("example.com/m.T.A")
	if err != nil {
		t.Fatalf("ResolveBeforeTarget: %v", err)
	}
	if tgt.Kind != "field" {
		t.Errorf("target.Kind = %q, want field", tgt.Kind)
	}
}

// TestBeforeSymbol：symbol 目标聚合 callers/impact（缺省其他组）。
func TestBeforeSymbol(t *testing.T) {
	a, _ := beforeActs(t)
	sum, err := a.Before("main")
	if err != nil {
		t.Fatalf("Before: %v", err)
	}
	if sum.Target.Kind != "symbol" {
		t.Errorf("target.Kind = %q", sum.Target.Kind)
	}
	if len(sum.Callers) == 0 || len(sum.Impact) == 0 {
		t.Errorf("symbol 目标应聚合 callers/impact（got callers=%v impact=%v）", sum.Callers, sum.Impact)
	}
	if sum.Writers != nil || sum.Relations != nil {
		t.Errorf("symbol 目标不应有字段/表组: %+v", sum)
	}
}

// TestBeforeField：字段目标聚合 writers/reads。
func TestBeforeField(t *testing.T) {
	a, _ := beforeActs(t)
	sum, err := a.Before("example.com/m.T.A")
	if err != nil {
		t.Fatalf("Before: %v", err)
	}
	if sum.Target.Kind != "field" {
		t.Errorf("target.Kind = %q", sum.Target.Kind)
	}
	if len(sum.Writers) == 0 || len(sum.Reads) == 0 {
		t.Errorf("字段目标应聚合 writers/reads: %+v", sum)
	}
}

// TestTraceFlow：trace 聚合 flows + chain。
func TestTraceFlow(t *testing.T) {
	a, _ := beforeActs(t)
	flow, err := a.TraceFlow("example.com/m.T.A", 8)
	if err != nil {
		t.Fatalf("TraceFlow: %v", err)
	}
	if flow.Target.Kind != "field" {
		t.Errorf("target.Kind = %q, want field", flow.Target.Kind)
	}
	if len(flow.Flows) == 0 {
		t.Errorf("trace 应含值流链: %+v", flow)
	}
}

// TestBatchSymbols：批量符号详情（Q244）——多输入一次返回。
func TestBatchSymbols(t *testing.T) {
	a, _ := beforeActs(t)
	res, err := a.BatchSymbols([]string{"main", "start"})
	if err != nil {
		t.Fatalf("BatchSymbols: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("results = %d, want 2", len(res))
	}
	if res[0].Name != "main" || res[1].Name != "start" {
		t.Errorf("顺序应保持输入: %+v", res)
	}
	if res[0].Callers == 0 {
		t.Errorf("main 应有调用者（start → main）: %+v", res[0])
	}
}
