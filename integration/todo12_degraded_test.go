//go:build integration

package integration

// todo12-13 R3：类型检查失败 ≠ 覆盖完整——build_metadata 记录包/类型检查
// 覆盖，sequence 查询携带 coverage + 独立诊断（与 SQL 解析统计分开）。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

func TestTodo12TypecheckCoverageRealIndex(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/app\n\ngo 1.21\n")
	write("bad/bad.go", `package bad

var Broken int = "not-an-int"
`)
	write("good/good.go", `package good

func Run() int { return 1 }
`)
	clearIndex(t, dir)
	_ = runCLI(t, "init", "--repo", dir, "--progress", "none")

	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	meta, err := sqlite.NewRepo(db).GetLatest()
	if err != nil || meta == nil {
		t.Fatalf("build_metadata 缺失: %v", err)
	}
	var stats map[string]any
	if err := json.Unmarshal([]byte(meta.DegradeStats), &stats); err != nil {
		t.Fatalf("degrade_stats 不是 JSON: %v\n%s", err, meta.DegradeStats)
	}
	if n, _ := stats["typecheck_errors"].(float64); n < 1 {
		t.Errorf("应记录类型检查错误数: %s", meta.DegradeStats)
	}
	if n, _ := stats["pkg_with_errors"].(float64); n < 1 {
		t.Errorf("应记录出错包数: %s", meta.DegradeStats)
	}
	if string(meta.Status) == "success" {
		t.Errorf("类型检查失败时状态不应为 success: %s", meta.Status)
	}
	// SQL 解析统计键仍在（分开记录）
	if _, ok := stats["sql_ast_ok"]; !ok {
		t.Errorf("SQL 解析统计应保留: %s", meta.DegradeStats)
	}

	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/app/good:Run",
		"--repo", dir, "--code", "--json")
	if code != 0 {
		t.Fatalf("sequence exit = %d; %s", code, out)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("sequence JSON: %v\n%s", err, out)
	}
	metaMap, _ := root["meta"].(map[string]any)
	cov, _ := metaMap["coverage"].(map[string]any)
	if cov == nil {
		t.Fatalf("查询应携带 coverage: %+v", metaMap)
	}
	if cov["build_status"] == "success" {
		t.Errorf("coverage 应反映降级状态: %+v", cov)
	}
	diags, _ := metaMap["diagnostics"].([]any)
	codes := map[string]bool{}
	for _, d := range diags {
		if m, ok := d.(map[string]any); ok {
			if c, _ := m["code"].(string); c != "" {
				codes[c] = true
			}
		}
	}
	if !codes["typecheck_errors"] {
		t.Errorf("应有 typecheck_errors 诊断: %v", codes)
	}
}
