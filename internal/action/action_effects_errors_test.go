package action

// todo12-8：被调写入与错误来源归入实际执行路径。
//   - Validate 失败路径无写入；Write 失败不含后续 Trace；Write 成功后
//     Trace 失败仍保留已发生写点与最终错误
//   - 错误来源区分 returned/swallowed/stale（不把所有重新赋值当吞错）
//   - 路径因未知调用不完整 → partial（任务 7 的 analysis.status）

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

const effectsErrorSrc = `package m

type Ledger struct{ State, TraceID string }
type Store interface{ Save(k, v string) error }

func Validate(k string) error { return nil }

func save(st Store, k, v string) error { return st.Save(k, v) }

func Commit(st Store, k, v string, l *Ledger) error {
	if err := Validate(k); err != nil {
		return err
	}
	l.State = "committed"
	if err := save(st, k, v); err != nil {
		return err
	}
	l.TraceID = "trace"
	return save(st, k, v)
}

func Stale(st Store) (error, error) {
	var domainErr, traceErr error
	domainErr = st.Save("a", "b")
	traceErr = st.Save("c", "d")
	return domainErr, traceErr
}

func Swallow(st Store) error {
	if err := st.Save("a", "b"); err != nil {
		err = nil
	}
	return nil
}
`

func lineOf(src, needle string) int {
	for i, ln := range strings.Split(src, "\n") {
		if strings.Contains(ln, needle) {
			return i + 1
		}
	}
	return 1
}

func seedEffectsErrorRepo(t *testing.T) (string, *Actions) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "flow.go"), []byte(effectsErrorSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	id := func(n string) domain.CanonicalID { return domain.CanonicalID("symbol:go:example.com/m:" + n) }
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:Ledger", Kind: domain.KindStruct, Name: "Ledger",
			FilePath: "flow.go", LineStart: lineOf(effectsErrorSrc, "type Ledger"),
			Properties: map[string]any{"fields": []map[string]any{
				{"name": "State"}, {"name": "TraceID"}}}},
		{ID: id("Validate"), Kind: domain.KindFunction, Name: "Validate",
			FilePath: "flow.go", LineStart: lineOf(effectsErrorSrc, "func Validate(")},
		{ID: id("save"), Kind: domain.KindFunction, Name: "save",
			FilePath: "flow.go", LineStart: lineOf(effectsErrorSrc, "func save(")},
		{ID: id("Commit"), Kind: domain.KindFunction, Name: "Commit",
			FilePath: "flow.go", LineStart: lineOf(effectsErrorSrc, "func Commit(")},
		{ID: id("Stale"), Kind: domain.KindFunction, Name: "Stale",
			FilePath: "flow.go", LineStart: lineOf(effectsErrorSrc, "func Stale(")},
		{ID: id("Swallow"), Kind: domain.KindFunction, Name: "Swallow",
			FilePath: "flow.go", LineStart: lineOf(effectsErrorSrc, "func Swallow(")},
	}
	fields := []*domain.FunctionFieldSummary{
		{FunctionID: id("Commit"), AccessKind: domain.SummaryDirectWrite,
			FieldPath: "example.com/m.Ledger.State", LineStart: lineOf(effectsErrorSrc, `l.State =`)},
		{FunctionID: id("Commit"), AccessKind: domain.SummaryDirectWrite,
			FieldPath: "example.com/m.Ledger.TraceID", LineStart: lineOf(effectsErrorSrc, `l.TraceID =`)},
	}
	if _, err := r.SaveBatchStats(nodes, nil, fields); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "fxerr", ToolName: "fixture", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	return dir, New(r)
}

// A1：三条路径的写点归属与最终错误。
func TestEffectsErrorPathsCalleeWrites(t *testing.T) {
	dir, acts := seedEffectsErrorRepo(t)
	res, err := acts.Effects(EffectsRequest{Target: "symbol:go:example.com/m:Commit",
		RepoAbs: dir, Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Paths) < 3 {
		t.Fatalf("应有三条路径: %+v", res.Paths)
	}
	// 校验失败路径：无写入；Write 失败路径：只有 State；成功路径：State+TraceID
	var none, onlyState, both *EffectPath
	for i := range res.Paths {
		p := &res.Paths[i]
		hasState, hasTrace := false, false
		for _, w := range p.Writes {
			if strings.Contains(w.Field, "State") {
				hasState = true
			}
			if strings.Contains(w.Field, "TraceID") {
				hasTrace = true
			}
		}
		switch {
		case !hasState && !hasTrace:
			none = p
		case hasState && !hasTrace:
			onlyState = p
		case hasState && hasTrace:
			both = p
		}
	}
	if none == nil || onlyState == nil || both == nil {
		t.Fatalf("路径应覆盖三种写点组合: %+v", res.Paths)
	}
	if none.Result == nil || len(none.Result.ErrorSources) == 0 ||
		none.Result.ErrorSources[0].Status == "unknown" {
		t.Errorf("校验失败路径应定位错误来源: %+v", none.Result)
	}
	if onlyState.Result == nil || len(onlyState.Result.ErrorSources) == 0 {
		t.Errorf("Write 失败路径应保留错误来源: %+v", onlyState.Result)
	}
	for _, w := range onlyState.Writes {
		if strings.Contains(w.Field, "TraceID") {
			t.Errorf("提前返回路径不得包含后续 Trace 写入: %+v", onlyState.Writes)
		}
	}
	if both.Result == nil || len(both.Result.ErrorSources) == 0 {
		t.Errorf("最终错误应出现在成功路径: %+v", both.Result)
	}
	if len(res.DBWrites) != 2 {
		t.Errorf("函数级汇总应保留两个字段写入: %+v", res.DBWrites)
	}
}

// A2：覆盖来源/吞错等级。
func TestEffectsErrorSourceStatuses(t *testing.T) {
	dir, acts := seedEffectsErrorRepo(t)
	stale, err := acts.Effects(EffectsRequest{Target: "symbol:go:example.com/m:Stale",
		RepoAbs: dir, Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(stale.Paths) != 1 || stale.Paths[0].Result == nil {
		t.Fatalf("Stale 应一条返回路径: %+v", stale.Paths)
	}
	src := stale.Paths[0].Result.ErrorSources
	var staleSrc *ErrorSource
	for i := range src {
		if src[i].Status == "stale" {
			staleSrc = &src[i]
		}
	}
	if staleSrc == nil || !strings.Contains(staleSrc.Detail, "最近的错误来源") {
		t.Errorf("返回旧错误应标 stale: %+v", src)
	}

	sw, err := acts.Effects(EffectsRequest{Target: "symbol:go:example.com/m:Swallow",
		RepoAbs: dir, Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	var swallowed *ErrorSource
	for _, p := range sw.Paths {
		if p.Result == nil {
			continue
		}
		for i := range p.Result.ErrorSources {
			if p.Result.ErrorSources[i].Status == "swallowed" {
				swallowed = &p.Result.ErrorSources[i]
			}
		}
	}
	if swallowed == nil {
		for _, p := range sw.Paths {
			if p.Result != nil {
				t.Logf("%s result=%+v", p.ID, p.Result)
			}
		}
		t.Errorf("返回 nil 且错误被覆盖应标 swallowed: %+v", sw.Paths)
	}
	if sw.Analysis == nil || sw.Analysis.Status != "partial" {
		t.Errorf("存在未映射调用点时应 partial: %+v", sw.Analysis)
	}
}
