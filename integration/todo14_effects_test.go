//go:build integration

package integration

// todo14-8：函数级错误汇总不得因变量名（failure/err）改变结论。
//   - 两个函数的顶层 result.suspicious 都标覆盖位置、verdict=unverified
//   - paths[].result.error_sources 均 swallowed + overwritten_at
//   - 无目标与 --target-return #0 两种模式一致

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
)

const todo14ErrorNamesSrc = `package regression

import "errors"

func FailureName() error {
	failure := errors.New("lost")
	failure = nil
	return failure
}

func ErrName() error {
	err := errors.New("lost")
	err = nil
	return err
}
`

func TestTodo14ErrorSummaryNameIndependent(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/codeintel-output-regression\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "err.go"), []byte(todo14ErrorNamesSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	base := "symbol:go:example.com/codeintel-output-regression:"
	query := func(sym string, extra ...string) action.EffectsResult {
		t.Helper()
		args := append([]string{"query", "effects", base + sym, "--repo", dir, "--depth", "3", "--json"}, extra...)
		code, out := runCLIOut(t, args...)
		if code != 0 {
			t.Fatalf("effects %s exit=%d; %s", sym, code, out)
		}
		var res action.EffectsResult
		if err := json.Unmarshal([]byte(out), &res); err != nil {
			t.Fatalf("effects JSON: %v\n%s", err, out)
		}
		return res
	}
	type summary struct {
		suspicious []string
		verdict    string
		pathStatus string
		pathLine   int
	}
	collect := func(res action.EffectsResult) summary {
		var s summary
		s.suspicious = res.Result.Suspicious
		s.verdict = res.Result.Verdict
		if len(res.Paths) != 1 || res.Paths[0].Result == nil ||
			len(res.Paths[0].Result.ErrorSources) == 0 {
			t.Fatalf("应有单路径错误来源: %+v", res.Paths)
		}
		es := res.Paths[0].Result.ErrorSources[0]
		s.pathStatus, s.pathLine = es.Status, es.OverwrittenAt
		return s
	}
	fail := collect(query("FailureName"))
	es := collect(query("ErrName"))
	if fail.pathStatus != "swallowed" || es.pathStatus != "swallowed" {
		t.Errorf("路径级应 swallowed: failure=%+v err=%+v", fail, es)
	}
	if len(fail.suspicious) == 0 || len(es.suspicious) == 0 {
		t.Errorf("汇总级应标记覆盖位置: failure=%+v err=%+v", fail, es)
	}
	if fail.verdict != "unverified" || es.verdict != "unverified" {
		t.Errorf("汇总 verdict 不应因变量名不同: failure=%+v err=%+v", fail, es)
	}
	if fail.pathLine == 0 || es.pathLine == 0 {
		t.Errorf("覆盖位置应为实际行号: failure=%+v err=%+v", fail, es)
	}
	// 无目标与返回槽目标一致（返回槽只保留错误依赖）
	rtFail := collect(query("FailureName", "--target-return", "#0"))
	rtErr := collect(query("ErrName", "--target-return", "#0"))
	if rtFail.pathStatus != "swallowed" || rtErr.pathStatus != "swallowed" ||
		rtFail.verdict != "unverified" || rtErr.verdict != "unverified" {
		t.Errorf("返回槽目标应保持同一错误结论: failure=%+v err=%+v", rtFail, rtErr)
	}
	// 文本可读
	code, text := runCLIOut(t, "query", "effects", base+"FailureName", "--repo", dir, "--depth", "3")
	if code != 0 || !strings.Contains(text, "可疑") || !strings.Contains(text, "swallowed") {
		t.Errorf("文本应说明覆盖/吞掉: code=%d\n%s", code, text)
	}
}
