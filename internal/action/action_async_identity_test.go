package action

// todo11-3：异步关联的身份与因果验证。
//   - 同名 ID 但**值来源不同** → 不得认定为同一生命周期（name_only/unverified）
//   - 值来源指纹一致 → value_origin（corroborated，身份传递可核验）
//   - 同一入口下互不调用的两个分支 → 不能因"都在展开集合"就判为可达

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// asyncIdentityFixture 建两个入口写同名 FlowID，但值来源不同（origins 不同）。
func asyncIdentityFixture(t *testing.T, sameOrigin bool) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package m

type Flow struct{ FlowID string }

func Approve(f *Flow) { f.FlowID = "x" }

func Sweep(f *Flow) { f.FlowID = "x" }

func Unrelated(f *Flow) { _ = f.FlowID }
`
	if err := os.WriteFile(filepath.Join(dir, "f.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:Approve", Kind: domain.KindFunction, Name: "Approve", FilePath: "f.go", LineStart: 5},
		{ID: "symbol:go:example.com/m:Sweep", Kind: domain.KindFunction, Name: "Sweep", FilePath: "f.go", LineStart: 7},
		{ID: "symbol:go:example.com/m:Unrelated", Kind: domain.KindFunction, Name: "Unrelated", FilePath: "f.go", LineStart: 9},
	}
	mkSum := func(fn string, writeLine int) *domain.FunctionFieldSummary {
		return &domain.FunctionFieldSummary{
			FunctionID: domain.CanonicalID("symbol:go:example.com/m:" + fn),
			AccessKind: domain.SummaryDirectWrite,
			FieldPath:  "example.com/m.Flow.FlowID",
			LineStart:  writeLine,
		}
	}
	// origins 走 SaveBatchStats 的显式参数（summary 行里的 Origins 不落库）
	mkOrigin := func(fn string, originLine int) *domain.SummaryOrigin {
		return &domain.SummaryOrigin{
			FunctionID: domain.CanonicalID("symbol:go:example.com/m:" + fn),
			AccessKind: domain.SummaryDirectWrite, FieldPath: "example.com/m.Flow.FlowID",
			CallLine: originLine, CalleeID: "symbol:go:example.com/m:Request",
		}
	}
	originSweep := 12
	if sameOrigin {
		originSweep = 11 // 与 Approve 相同 → 值来源一致
	}
	sums := []*domain.FunctionFieldSummary{
		mkSum("Approve", 6),
		mkSum("Sweep", 8),
	}
	origins := []*domain.SummaryOrigin{
		mkOrigin("Approve", 11),
		mkOrigin("Sweep", originSweep),
	}
	if _, err := r.SaveBatchStats(nodes, nil, sums, origins); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "id1", ToolName: "test", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	return New(r), dir
}

// TestAsyncNameOnlyDifferentOrigin：同名 ID 但来源不同 → name_only/unverified。
func TestAsyncNameOnlyDifferentOrigin(t *testing.T) {
	acts, dir := asyncIdentityFixture(t, false)
	res, err := acts.AsyncLifecycle(AsyncLifecycleRequest{Seed: "symbol:go:example.com/m:Approve", RepoAbs: dir})
	if err != nil {
		t.Fatal(err)
	}
	var link *AsyncLink
	for i := range res.Links {
		if res.Links[i].From == "symbol:go:example.com/m:Approve" &&
			res.Links[i].To == "symbol:go:example.com/m:Sweep" {
			link = &res.Links[i]
		}
	}
	if link == nil {
		t.Fatalf("应有 Approve↔Sweep 关联: %+v", res.Links)
	}
	if link.EvidenceKind != "name_only" || link.Verdict != SeqVerdictUnverified {
		t.Errorf("同名不同源必须降为 name_only/unverified: %+v", link)
	}
	if !contains(link.Detail, "值来源不同") {
		t.Errorf("说明应指出值来源不同（不能认定同一生命周期）: %+v", link)
	}
}

// TestAsyncValueOriginSame：同名 ID 且来源指纹一致 → value_origin（corroborated）。
func TestAsyncValueOriginSame(t *testing.T) {
	acts, dir := asyncIdentityFixture(t, true)
	res, err := acts.AsyncLifecycle(AsyncLifecycleRequest{Seed: "symbol:go:example.com/m:Approve", RepoAbs: dir})
	if err != nil {
		t.Fatal(err)
	}
	var link *AsyncLink
	for i := range res.Links {
		if res.Links[i].From == "symbol:go:example.com/m:Approve" &&
			res.Links[i].To == "symbol:go:example.com/m:Sweep" {
			link = &res.Links[i]
		}
	}
	if link == nil {
		t.Fatalf("应有 Approve↔Sweep 关联: %+v", res.Links)
	}
	if link.EvidenceKind != "value_origin" || link.Verdict != SeqVerdictCorroborated {
		t.Errorf("来源指纹一致应升级为 value_origin/corroborated: %+v", link)
	}
}

// TestAsyncExpansionNotReachability：同入口下的两个分支都在展开集合里，但
// 彼此不调用 → 不得判为 call_path 可达。
func TestAsyncExpansionNotReachability(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package m

type Flow struct{ State string }

func AuditA(f *Flow) { f.State = "a" }

func AuditB(f *Flow) { f.State = "b" }

func Entry(f *Flow) {
	if f.State == "" {
		AuditA(f)
	} else {
		AuditB(f)
	}
}
`
	if err := os.WriteFile(filepath.Join(dir, "f.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:Entry", Kind: domain.KindFunction, Name: "Entry", FilePath: "f.go", LineStart: 9},
		{ID: "symbol:go:example.com/m:AuditA", Kind: domain.KindFunction, Name: "AuditA", FilePath: "f.go", LineStart: 5},
		{ID: "symbol:go:example.com/m:AuditB", Kind: domain.KindFunction, Name: "AuditB", FilePath: "f.go", LineStart: 7},
	}
	facts := []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:Entry", TargetID: "symbol:go:example.com/m:AuditA",
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": 11}},
		{SourceID: "symbol:go:example.com/m:Entry", TargetID: "symbol:go:example.com/m:AuditB",
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": 13}},
	}
	sums := []*domain.FunctionFieldSummary{
		{FunctionID: "symbol:go:example.com/m:AuditA", AccessKind: domain.SummaryDirectWrite,
			FieldPath: "example.com/m.Flow.State", LineStart: 5},
		{FunctionID: "symbol:go:example.com/m:AuditB", AccessKind: domain.SummaryDirectWrite,
			FieldPath: "example.com/m.Flow.State", LineStart: 7},
	}
	if _, err := r.SaveBatchStats(nodes, facts, sums); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "id2", ToolName: "test", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	res, err := New(r).AsyncLifecycle(AsyncLifecycleRequest{
		Seed: "symbol:go:example.com/m:Entry", RepoAbs: dir})
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range res.Links {
		if l.EvidenceKind == "call_path" &&
			((contains(l.From, "AuditA") && contains(l.To, "AuditB")) ||
				(contains(l.From, "AuditB") && contains(l.To, "AuditA"))) {
			t.Errorf("同入口下的互斥分支不得判为调用路径可达: %+v", l)
		}
	}
}

// contains 字符串包含（测试小工具）。
func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
