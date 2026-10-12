package action

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedRepo 建临时仓库 + 预填一个小图（action 测试用）。
func seedRepo(t *testing.T) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	mainID := domain.CanonicalID("symbol:go:example.com/m:main")
	runID := domain.CanonicalID("symbol:go:example.com/m/svc:(Svc).Run")
	nodes := []*domain.CodeEntity{
		{ID: mainID, Kind: domain.KindFunction, Name: "main", FilePath: "main.go", LineStart: 3},
		{ID: runID, Kind: domain.KindMethod, Name: "(Svc).Run", FilePath: "svc/svc.go", LineStart: 5},
		{ID: "symbol:go:example.com/m:helper", Kind: domain.KindFunction, Name: "helper", FilePath: "helper.go"},
	}
	if _, err := r.SaveBatchStats(nodes, []*domain.Fact{
		{SourceID: mainID, TargetID: runID, Kind: domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.9},
		{SourceID: mainID, TargetID: "symbol:go:example.com/m:helper", Kind: domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.9},
	}, nil); err != nil {
		t.Fatalf("save: %v", err)
	}
	// 字段摘要 + 追溯图（fields/trace/export 用）
	funcID := string(mainID)
	r.SaveBatchStats(nil, nil, []*domain.FunctionFieldSummary{
		{FunctionID: domain.CanonicalID(funcID), AccessKind: domain.SummaryDirectWrite,
			FieldPath: "example.com/m.T.A", InstancePath: "t.A", LineStart: 5, CodeSnippet: "t.A = v"},
		{FunctionID: domain.CanonicalID(funcID), AccessKind: domain.SummaryDirectRead,
			FieldPath: "example.com/m.T.A", InstancePath: "t.A", LineStart: 7, CodeSnippet: "return t.A"},
	})
	writeNode := &domain.CodeEntity{ID: domain.CanonicalID(funcID + "#t.A.write@5"),
		Kind: domain.KindFieldAccess, Name: "t.A", FilePath: "main.go", LineStart: 5,
		Properties: map[string]any{"full_path": "example.com/m.T.A", "instance_path": "t.A",
			"access_kind": "write", "func_id": funcID}}
	val := &domain.CodeEntity{ID: domain.CanonicalID(funcID + "#t0"), Kind: domain.KindSSAValue,
		Name: "t0", Properties: map[string]any{"func_id": funcID}}
	r.SaveBatchStats([]*domain.CodeEntity{writeNode, val}, []*domain.Fact{
		{SourceID: val.ID, TargetID: writeNode.ID, Kind: domain.FactDataFlowsTo, ToolSource: domain.ToolSSA, Confidence: 1},
	}, nil)
	return New(r), dir
}

func TestResolveSymbol(t *testing.T) {
	a, _ := seedRepo(t)
	// canonical ID 直连
	n, err := a.ResolveSymbol("symbol:go:example.com/m:main")
	if err != nil || n.Name != "main" {
		t.Errorf("resolve id = %v, %v", n, err)
	}
	// 名称精确
	n, err = a.ResolveSymbol("main")
	if err != nil || n.Name != "main" {
		t.Errorf("resolve name = %v, %v", n, err)
	}
	// 不存在
	if _, err := a.ResolveSymbol("nope_nope"); err == nil {
		t.Error("resolve unknown should fail")
	}
}

func TestSymbolDetail(t *testing.T) {
	a, _ := seedRepo(t)
	d, err := a.SymbolDetail("main")
	if err != nil {
		t.Fatal(err)
	}
	if d.Node.Name != "main" {
		t.Errorf("node = %v", d.Node.Name)
	}
	if len(d.Callees) != 2 {
		t.Errorf("callees = %d, want 2", len(d.Callees))
	}
}

func TestCallersCalleesImpact(t *testing.T) {
	a, _ := seedRepo(t)
	run := domain.CanonicalID("symbol:go:example.com/m/svc:(Svc).Run")
	callers, err := a.Callers(run, 1)
	if err != nil || len(callers) != 1 {
		t.Errorf("callers = %v, %v", callers, err)
	}
	callees, err := a.Callees(domain.CanonicalID("symbol:go:example.com/m:main"), 1)
	if err != nil || len(callees) != 2 {
		t.Errorf("callees = %v, %v", callees, err)
	}
	nodes, err := a.Impact(run, 3)
	if err != nil || len(nodes) == 0 {
		t.Errorf("impact = %v, %v", nodes, err)
	}
}

func TestFunctionFieldsAndTrace(t *testing.T) {
	a, _ := seedRepo(t)
	n, rows, err := a.FunctionFields("main")
	if err != nil || n.Name != "main" {
		t.Fatalf("fields = %v, %v", n, err)
	}
	if len(rows) != 2 {
		t.Errorf("rows = %d, want 2", len(rows))
	}
	tn, tr, err := a.Trace(TraceParams{Field: "example.com/m.T.A", Func: "main", Forward: false})
	if err != nil {
		t.Fatal(err)
	}
	if tn.Name != "main" || len(tr) == 0 {
		t.Errorf("trace-backward = %v, %d rows", tn, len(tr))
	}
}

func TestValueTraceSearchExpandFlows(t *testing.T) {
	a, _ := seedRepo(t)
	vt, err := a.ValueTrace("symbol:go:example.com/m:main#t0", 8, 0, false)
	if err != nil || len(vt) == 0 {
		t.Errorf("value-trace = %v, %v", vt, err)
	}
	s, err := a.Search("main", "")
	if err != nil || len(s) == 0 {
		t.Errorf("search = %v, %v", s, err)
	}
	cur, facts, nodes, err := a.Expand(domain.CanonicalID("symbol:go:example.com/m:main"))
	if err != nil || cur.Name != "main" {
		t.Errorf("expand = %v, %v", cur, err)
	}
	if len(facts) == 0 || len(nodes) == 0 {
		t.Errorf("expand facts/nodes = %d/%d", len(facts), len(nodes))
	}
	flows, err := a.Flows(domain.CanonicalID("symbol:go:example.com/m:main"), 8)
	if err != nil || len(flows) == 0 {
		t.Errorf("flows = %v, %v", flows, err)
	}
}

// TestCallersConcreteIface：接口反向具体化回归——查实现方法的调用者时，
// 经接口方法发出的首跳调用应映射回该实现方法（CallersConcrete）。
func TestCallersConcreteIface(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)

	ifaceID := domain.CanonicalID("symbol:go:example.com/m/id:IStoreHandler")
	ifaceM := domain.CanonicalID("symbol:go:example.com/m/id:(IStoreHandler).Check")
	implType := domain.CanonicalID("symbol:go:example.com/m/biz:StoreBiz")
	implM := domain.CanonicalID("symbol:go:example.com/m/biz:(StoreBiz).Check")
	caller := domain.CanonicalID("symbol:go:example.com/m:caller")
	direct := domain.CanonicalID("symbol:go:example.com/m:direct")
	nodes := []*domain.CodeEntity{
		{ID: ifaceID, Kind: domain.KindInterface, Name: "IStoreHandler"},
		{ID: ifaceM, Kind: domain.KindMethod, Name: "(IStoreHandler).Check"},
		{ID: implType, Kind: domain.KindStruct, Name: "StoreBiz"},
		{ID: implM, Kind: domain.KindMethod, Name: "(StoreBiz).Check", FilePath: "biz/biz.go", LineStart: 10},
		{ID: caller, Kind: domain.KindFunction, Name: "caller"},
		{ID: direct, Kind: domain.KindFunction, Name: "direct"},
	}
	edges := []*domain.Fact{
		{SourceID: ifaceID, TargetID: implType, Kind: domain.FactImplements, ToolSource: domain.ToolSCIP, Confidence: 1},
		{SourceID: caller, TargetID: ifaceM, Kind: domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.8,
			Metadata: map[string]any{"line_num": 20}},
		{SourceID: direct, TargetID: implM, Kind: domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.8},
	}
	if _, err := r.SaveBatchStats(nodes, edges, nil); err != nil {
		t.Fatalf("save: %v", err)
	}
	a := New(r)
	facts, err := a.CallersConcrete(implM, 1)
	if err != nil {
		t.Fatal(err)
	}
	var viaIface, directHit bool
	for _, f := range facts {
		if f.SourceID == caller && f.TargetID == implM {
			viaIface = true
		}
		if f.SourceID == direct && f.TargetID == implM {
			directHit = true
		}
	}
	if !viaIface {
		t.Errorf("CallersConcrete 未回溯接口调用者，facts=%v", facts)
	}
	if !directHit {
		t.Errorf("CallersConcrete 丢失直接调用者，facts=%v", facts)
	}
	// 接口类型形态（未记录方法名）不猜测：查接口方法本身不应产生映射
	if got := a.repo.ConcreteMethodIfaces(string(ifaceM)); len(got) != 0 {
		t.Errorf("接口方法不应有反向映射，got=%v", got)
	}
}
