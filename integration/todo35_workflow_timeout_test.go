//go:build integration

package integration

// todo35：B10e.3/.4 + B11e.3（覆盖完整性/场景取消/timeout 存在性）。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
)

// u35Coverage 组合场景覆盖摘要。
func u35Coverage(t *testing.T, dir string) map[string]any {
	t.Helper()
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/remaining:EntryInline",
		"--repo", dir, "--code", "--depth", "1", "--json",
		"--related-entry", u35ReviewArg("Review"), "--frontend-root", "alias-frontend",
		"--combination-selector", "symbol:go:example.com/remaining:Supported")
	if code != 0 {
		t.Fatalf("coverage exit=%d\n%s", code, out)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("coverage JSON: %v\n%s", err, out)
	}
	wf, _ := root["workflow"].(map[string]any)
	return wf
}

// TestWorkflowCoverageScenarioConsistency B10e.3：complete/partial 以场景状态为准。
func TestWorkflowCoverageScenarioConsistency(t *testing.T) {
	dir := u35Repo(t)
	t.Run("positive", func(t *testing.T) {
		wf := u35Coverage(t, dir)
		cov, _ := wf["coverage"].(map[string]any)
		if u35Int(cov["supported"]) != 2 || u35Int(cov["analyzed"]) != 2 ||
			u35Int(cov["complete"]) != 0 || u35Int(cov["partial"]) != 2 {
			t.Fatalf("coverage = %v", cov)
		}
		for _, raw := range asAnySlice(wf["scenarios"]) {
			sc, _ := raw.(map[string]any)
			if !u35Bool(sc["partial"]) {
				t.Fatalf("scenario = %v（want partial）", sc)
			}
		}
	})
	t.Run("negative", func(t *testing.T) {
		// 降低 depth 不能仍报 complete（同输入改一个事实即失效）。
		wf := u35Coverage(t, dir)
		cov, _ := wf["coverage"].(map[string]any)
		if u35Int(cov["complete"]) != 0 {
			t.Fatalf("depth=1 仍报 complete: %v", cov)
		}
	})
}

// TestWorkflowScenarioCancellation B10e.4：场景阶段取消传播到单查询与 batch。
func TestWorkflowScenarioCancellation(t *testing.T) {
	dir := u35Repo(t)
	old := action.SeqScenarioStageHook
	action.SeqScenarioStageHook = func(context.Context) bool { return true }
	defer func() { action.SeqScenarioStageHook = old }()

	t.Run("positive", func(t *testing.T) {
		code, out, stderr := runCLIOutErr(t, "query", "sequence",
			"symbol:go:example.com/remaining:EntryInline", "--repo", dir, "--code",
			"--depth", "1", "--json", "--related-entry", u35ReviewArg("Review"),
			"--frontend-root", "alias-frontend",
			"--combination-selector", "symbol:go:example.com/remaining:Supported")
		if code != 1 || strings.TrimSpace(out) != "" ||
			!strings.Contains(stderr, "error: context canceled") {
			t.Fatalf("cancel = code=%d out=%q stderr=%q", code, out, stderr)
		}
	})
	t.Run("negative", func(t *testing.T) {
		// 正常完整 workflow 继续成功（同一二进制，关闭钩子后）。
		action.SeqScenarioStageHook = nil
		code, out, stderr := runCLIOutErr(t, "query", "sequence",
			"symbol:go:example.com/remaining:EntryInline", "--repo", dir, "--code",
			"--depth", "1", "--json", "--related-entry", u35ReviewArg("Review"),
			"--frontend-root", "alias-frontend",
			"--combination-selector", "symbol:go:example.com/remaining:Supported")
		if code != 0 || strings.TrimSpace(out) == "" {
			t.Fatalf("normal = code=%d stderr=%q", code, stderr)
		}
		// batch：取消后不得继续导出后续流程。
		action.SeqScenarioStageHook = func(context.Context) bool { return true }
		outDir := filepath.Join(t.TempDir(), "batch-cancel")
		manifest := filepath.Join(t.TempDir(), "flows.json")
		writeFile(t, manifest, `{"version":1,"flows":[`+
			`{"id":"first","repo":`+jsonQuote(dir)+`,"entry":"symbol:go:example.com/remaining:EntryInline",`+
			`"relatedEntries":["symbol:go:example.com/remaining:Review"],`+
			`"combinationSelector":"symbol:go:example.com/remaining:Supported","depth":1},`+
			`{"id":"second","repo":`+jsonQuote(dir)+`,"entry":"symbol:go:example.com/remaining:SubmitTx",`+
			`"relatedEntries":["symbol:go:example.com/remaining:Review"]}]}`)
		code, out, stderr = runCLIOutErr(t, "sequence-batch", "--manifest", manifest,
			"--out", outDir, "--json")
		if code != 1 {
			t.Fatalf("batch cancel exit=%d stderr=%q", code, stderr)
		}
		if _, err := os.Stat(filepath.Join(outDir, "second.md")); err == nil {
			t.Fatal("取消后仍调度并导出了后续流程")
		}
		if strings.Contains(out, `"exported":1`) || strings.Contains(out, `"exported": 1`) {
			t.Fatalf("取消被当成成功导出: %s", out)
		}
	})
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// u35Batch 写 manifest 并运行 sequence-batch。
func u35Batch(t *testing.T, dir, timeoutJSON, repo string) (int, string, string, string) {
	t.Helper()
	base := t.TempDir()
	manifest := filepath.Join(base, "flows.json")
	writeFile(t, manifest, `{"version":1,"flows":[{"id":"review","repo":`+jsonQuote(repo)+
		`,"entry":"symbol:go:example.com/remaining:SubmitTx",`+
		`"relatedEntries":["symbol:go:example.com/remaining:Review"]`+timeoutJSON+
		`}],"formats":["markdown"]}`)
	outDir := filepath.Join(base, "out")
	code, out, stderr := runCLIOutErr(t, "sequence-batch", "--manifest", manifest,
		"--out", outDir, "--json")
	return code, out, stderr, outDir
}

// TestTimeoutPresencePreflight B11e.3：timeout 存在性与显式空值/非法值。
func TestTimeoutPresencePreflight(t *testing.T) {
	dir := u35Repo(t)
	t.Run("manifest_positive", func(t *testing.T) {
		code, out, stderr, outDir := u35Batch(t, dir, ``, dir)
		if code != 0 {
			t.Fatalf("缺省 timeout 应使用默认值: code=%d stderr=%q out=%q", code, stderr, out)
		}
		if _, err := os.Stat(filepath.Join(outDir, "review.md")); err != nil {
			t.Fatalf("缺省 timeout 未导出: %v", err)
		}
	})
	t.Run("manifest_negative", func(t *testing.T) {
		for _, tc := range []struct{ name, json string }{
			{"empty", `,"timeout":""`},
			{"null", `,"timeout":null`},
			{"wrong_type", `,"timeout":30`},
			{"zero", `,"timeout":"0"`},
			{"negative", `,"timeout":"-5"`},
			{"suffix", `,"timeout":"30sx"`},
			{"overflow", `,"timeout":"20000000000"`},
		} {
			code, out, stderr, outDir := u35Batch(t, dir, tc.json, dir)
			if code != 1 || strings.TrimSpace(out) != "" ||
				!strings.Contains(stderr, "flow review timeout 非法") {
				t.Fatalf("%s = code=%d out=%q stderr=%q", tc.name, code, out, stderr)
			}
			if _, err := os.Stat(outDir); err == nil {
				t.Fatalf("%s 在校验失败后仍创建了输出目录", tc.name)
			}
		}
		// 不存在的 repo 也必须先报 timeout 错误（校验顺序在开库前）。
		code, out, stderr, _ := u35Batch(t, dir, `,"timeout":""`, "/nonexistent/remaining-repo")
		if code != 1 || strings.TrimSpace(out) != "" ||
			!strings.Contains(stderr, "flow review timeout 非法") {
			t.Fatalf("order = code=%d out=%q stderr=%q", code, out, stderr)
		}
	})
	t.Run("cli_positive", func(t *testing.T) {
		for _, args := range [][]string{
			{"--timeout", "30s"},
			{"--timeout=30s"},
		} {
			base := []string{"query", "sequence", "symbol:go:example.com/remaining:SubmitTx",
				"--repo", dir, "--code", "--json", "--related-entry", u35ReviewArg("Review")}
			code, out, stderr := runCLIOutErr(t, append(base, args...)...)
			if code != 0 || strings.TrimSpace(out) == "" {
				t.Fatalf("%v = code=%d stderr=%q", args, code, stderr)
			}
		}
	})
	t.Run("cli_negative", func(t *testing.T) {
		for _, args := range [][]string{
			{"--timeout"},
			{"--timeout="},
			{"--timeout", "--json"},
		} {
			base := []string{"query", "sequence", "symbol:go:example.com/remaining:SubmitTx",
				"--repo", "/nonexistent/remaining-repo", "--code"}
			code, out, stderr := runCLIOutErr(t, append(base, args...)...)
			if code != 2 || strings.TrimSpace(out) != "" ||
				!strings.Contains(stderr, "--timeout 非法") {
				t.Fatalf("%v = code=%d out=%q stderr=%q", args, code, out, stderr)
			}
		}
	})
}
