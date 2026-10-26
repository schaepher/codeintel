package action

// todo13-8：return 表达式中的被调写入 + 错误来源按值身份分析。
//   - return TraceError(l,t) 的 TraceID 写入必须归入该路径（writes 0/1/2）
//   - 返回被调函数的错误值可定位（returned_call）
//   - `failure := errors.New(); failure = nil; return failure` 标覆盖，
//     不把原错误当最终返回错误；改名为 err 结论一致

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

const effectsReturnCalleeSrc = `package example

import "errors"

type Ledger struct {
	State   string
	TraceID string
}

func ValidateError(b bool) error {
	if b {
		return errors.New("invalid")
	}
	return nil
}

func WriteError(l *Ledger, b bool) error {
	l.State = "written"
	if b {
		return errors.New("write")
	}
	return nil
}

func TraceError(l *Ledger, b bool) error {
	l.TraceID = "traced"
	if b {
		return errors.New("trace")
	}
	return nil
}

func CommitErrors(l *Ledger, v, w, t bool) error {
	if err := ValidateError(v); err != nil {
		return err
	}
	if err := WriteError(l, w); err != nil {
		return err
	}
	return TraceError(l, t)
}

func LostFailure() error {
	failure := errors.New("lost")
	failure = nil
	return failure
}

func LostErr() error {
	err := errors.New("lost")
	err = nil
	return err
}
`

func effectsReturnCalleeFixture(t *testing.T) (string, *Actions) {
	t.Helper()
	base := "symbol:go:example.com/effects:"
	fn := func(name string) domain.CanonicalID { return domain.CanonicalID(base + name) }
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/effects:Ledger", Kind: domain.KindStruct, Name: "Ledger",
			FilePath: "flow.go", LineStart: 5,
			Properties: map[string]any{"fields": []map[string]any{
				{"name": "State"}, {"name": "TraceID"}}}},
		{ID: fn("CommitErrors"), Kind: domain.KindFunction, Name: "CommitErrors",
			FilePath: "flow.go", LineStart: 33},
		{ID: fn("ValidateError"), Kind: domain.KindFunction, Name: "ValidateError",
			FilePath: "flow.go", LineStart: 10},
		{ID: fn("WriteError"), Kind: domain.KindFunction, Name: "WriteError",
			FilePath: "flow.go", LineStart: 17},
		{ID: fn("TraceError"), Kind: domain.KindFunction, Name: "TraceError",
			FilePath: "flow.go", LineStart: 25},
		{ID: fn("LostFailure"), Kind: domain.KindFunction, Name: "LostFailure",
			FilePath: "flow.go", LineStart: 43},
		{ID: fn("LostErr"), Kind: domain.KindFunction, Name: "LostErr",
			FilePath: "flow.go", LineStart: 49},
	}
	facts := []*domain.Fact{
		callFact(base+"CommitErrors", base+"ValidateError", 34),
		callFact(base+"CommitErrors", base+"WriteError", 37),
		callFact(base+"CommitErrors", base+"TraceError", 40),
	}
	fields := []*domain.FunctionFieldSummary{
		{FunctionID: domain.CanonicalID(base + "WriteError"), AccessKind: domain.SummaryDirectWrite,
			FieldPath: "example.com/effects.Ledger.State", LineStart: 18},
		{FunctionID: domain.CanonicalID(base + "TraceError"), AccessKind: domain.SummaryDirectWrite,
			FieldPath: "example.com/effects.Ledger.TraceID", LineStart: 26},
	}
	return seedEffectsRepo(t, effectsReturnCalleeSrc, nodes, fields, facts)
}

func callFact(from, to string, line int) *domain.Fact {
	return &domain.Fact{SourceID: domain.CanonicalID(from), TargetID: domain.CanonicalID(to),
		Kind: domain.FactCalls, Confidence: 0.8,
		Metadata: map[string]any{"line_num": float64(line)}}
}

func writesSummary(p EffectPath) string {
	var xs []string
	for _, w := range p.Writes {
		xs = append(xs, fieldLeaf(w.Field)+"@"+itoaSeq(w.Line))
	}
	return strings.Join(xs, ",")
}

func TestEffectsReturnExpressionCalleeWrites(t *testing.T) {
	dir, acts := effectsReturnCalleeFixture(t)
	res := mustEffects(t, acts, EffectsRequest{
		Target: "symbol:go:example.com/effects:CommitErrors", RepoAbs: dir, Depth: 2})
	// 三条终态：Validate 失败 / Write 失败 / 走到 return TraceError
	if len(res.Paths) != 3 {
		t.Fatalf("应有 3 条调用方终态路径，实为 %d: %+v", len(res.Paths), res.Paths)
	}
	counts := map[int]int{}
	for _, p := range res.Paths {
		counts[len(p.Writes)]++
	}
	if counts[0] != 1 || counts[1] != 1 || counts[2] != 1 {
		t.Fatalf("三条路径 writes 数应为 [0,1,2]，实为 %v: %+v", counts, res.Paths)
	}
	var final *EffectPath
	for i := range res.Paths {
		p := &res.Paths[i]
		if len(p.Writes) == 2 {
			final = p
			break
		}
	}
	if final == nil {
		t.Fatal("缺少走到 return TraceError 的路径")
	}
	got := writesSummary(*final)
	if !strings.Contains(got, "State@") || !strings.Contains(got, "TraceID@") {
		t.Errorf("最终路径应同时含 State/TraceID 写入（来源为两个调用点）: %s", got)
	}
	if final.Result == nil || final.Result.ErrorSources == nil {
		t.Fatalf("应给出错误来源: %+v", final.Result)
	}
	es := final.Result.ErrorSources[0]
	if es.Status != "returned_call" || !strings.Contains(es.Detail, "TraceError") || es.Line == 0 {
		t.Errorf("返回 TraceError(...) 的错误来源应为被调返回: %+v", es)
	}
	// 被调写入按调用点归属（CallLine = 真实调用位置）
	for _, w := range final.Writes {
		if w.Origin != "callee" || w.Evidence == nil || w.Evidence.CallLine == 0 {
			t.Errorf("被调写入应带调用点位置: %+v", w)
		}
	}
	// Validate 失败路径无写入
	for _, p := range res.Paths {
		if len(p.Writes) == 0 {
			if p.Result == nil || p.Result.ErrorSources == nil {
				t.Errorf("Validate 失败路径应记录错误来源: %+v", p)
			}
		}
	}
}

func TestEffectsErrorSourceByValueIdentity(t *testing.T) {
	dir, acts := effectsReturnCalleeFixture(t)
	failure := mustEffects(t, acts, EffectsRequest{
		Target: "symbol:go:example.com/effects:LostFailure", RepoAbs: dir, Depth: 1})
	renamed := mustEffects(t, acts, EffectsRequest{
		Target: "symbol:go:example.com/effects:LostErr", RepoAbs: dir, Depth: 1})
	get := func(r *EffectsResult) ErrorSource {
		if len(r.Paths) != 1 || r.Paths[0].Result == nil || len(r.Paths[0].Result.ErrorSources) == 0 {
			t.Fatalf("应有单路径错误来源: %+v", r.Paths)
		}
		return r.Paths[0].Result.ErrorSources[0]
	}
	a := get(failure)
	b := get(renamed)
	if a.Status != "swallowed" || a.OverwrittenAt == 0 {
		t.Errorf("failure 非 nil 后被 nil 覆盖应标 swallowed + 覆盖位置: %+v", a)
	}
	if a.Var != "failure" && a.Var != "err" {
		t.Errorf("应记录实际变量身份: %+v", a)
	}
	if b.Status != a.Status || (b.OverwrittenAt > 0) != (a.OverwrittenAt > 0) || b.Detail != a.Detail {
		t.Errorf("改名不应改变结论: failure=%+v err=%+v", a, b)
	}
	if a.Status == "returned" {
		t.Errorf("不得把原非 nil 错误当作最终返回错误: %+v", a)
	}
}
