//go:build integration

package integration

// todo13-8 A3：生产索引 → CLI（无目标/字段目标/返回槽目标）。
//   - CommitErrors 三条调用方路径 writes 0/1/2（return TraceError 的写入不再丢失）
//   - 字段目标只投影相关写入；返回槽目标保留错误依赖
//   - failure := errors.New(); failure=nil; return failure → swallowed + 覆盖位置

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
)

const todo13EffectsSrc = `package regression

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
`

func TestTodo13EffectsReturnCalleeRealIndex(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/codeintel-regression\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "effects.go"), []byte(todo13EffectsSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	base := "symbol:go:example.com/codeintel-regression:"
	query := func(args ...string) action.EffectsResult {
		t.Helper()
		all := append([]string{"query", "effects"}, args...)
		all = append(all, "--repo", dir, "--json")
		code, out := runCLIOut(t, all...)
		if code != 0 {
			t.Fatalf("effects exit = %d; %s", code, out)
		}
		var res action.EffectsResult
		if err := json.Unmarshal([]byte(out), &res); err != nil {
			t.Fatalf("effects JSON: %v\n%s", err, out)
		}
		return res
	}

	res := query(base+"CommitErrors", "--depth", "3")
	if len(res.Paths) != 3 {
		t.Fatalf("应有 3 条调用方终态: %+v", res.Paths)
	}
	counts := map[int]int{}
	for _, p := range res.Paths {
		counts[len(p.Writes)]++
	}
	if counts[0] != 1 || counts[1] != 1 || counts[2] != 1 {
		t.Fatalf("writes 数应为 [0,1,2]，实为 %v: %+v", counts, res.Paths)
	}
	var tracePath *action.EffectPath
	for i := range res.Paths {
		if len(res.Paths[i].Writes) == 2 {
			tracePath = &res.Paths[i]
		}
	}
	if tracePath == nil {
		t.Fatal("缺少 Trace 路径")
	}
	fields := map[string]bool{}
	for _, w := range tracePath.Writes {
		fields[strings.ToUpper(w.Field)] = true
		if w.Evidence == nil || w.Evidence.CallLine == 0 {
			t.Errorf("被调写入应带真实调用位置: %+v", w)
		}
	}
	joined := strings.Join(mapsKeys(fields), ",")
	if !strings.Contains(joined, "STATE") || !strings.Contains(joined, "TRACEID") {
		t.Errorf("Trace 路径应含 State 与 TraceID: %+v", tracePath.Writes)
	}
	if tracePath.Result == nil || len(tracePath.Result.ErrorSources) == 0 ||
		tracePath.Result.ErrorSources[0].Status != "returned_call" {
		t.Errorf("Trace 路径错误来源应为 returned_call: %+v", tracePath.Result)
	}

	// 字段目标：只投影 TraceID
	ft := query(base+"CommitErrors", "--depth", "3",
		"--target-field", "example.com/codeintel-regression.Ledger.TraceID")
	for _, p := range ft.Paths {
		for _, w := range p.Writes {
			if !strings.Contains(w.Field, "TraceID") {
				t.Errorf("字段目标不得带回无关写入: %+v", w)
			}
		}
	}
	// 返回槽目标：保留错误依赖
	rt := query(base+"CommitErrors", "--depth", "3", "--target-return", "#0")
	foundErr := false
	for _, p := range rt.Paths {
		if p.Result != nil && len(p.Result.ErrorSources) > 0 {
			foundErr = true
		}
	}
	if !foundErr {
		t.Errorf("返回槽目标应保留错误来源: %+v", rt.Paths)
	}

	// 覆盖：原非 nil 错误被赋 nil，最终返回 nil
	lf := query(base+"LostFailure", "--depth", "1")
	if len(lf.Paths) != 1 || lf.Paths[0].Result == nil || len(lf.Paths[0].Result.ErrorSources) == 0 {
		t.Fatalf("LostFailure 应有错误来源: %+v", lf.Paths)
	}
	es := lf.Paths[0].Result.ErrorSources[0]
	if es.Status != "swallowed" || es.OverwrittenAt == 0 {
		t.Errorf("应标明原错误被覆盖及位置: %+v", es)
	}
	if es.Status == "returned" {
		t.Errorf("不得把原错误当作最终返回错误: %+v", es)
	}
	// 文本同口径（无 --json）
	code, out := runCLIOut(t, "query", "effects", base+"CommitErrors", "--repo", dir,
		"--depth", "3")
	if code != 0 || !strings.Contains(out, "returned_call") {
		t.Errorf("文本应说明错误来源: code=%d\n%s", code, out)
	}
}

func mapsKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
