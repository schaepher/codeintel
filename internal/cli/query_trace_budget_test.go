package cli

// todo13-17：value-trace 预算/取消 + 阶段计数 + 缓存按索引身份失效。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

func seedValueTraceCLI(t *testing.T) (string, *action.Actions) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package m\n\nfunc Fn() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:(T).Fn", Kind: domain.KindMethod, Name: "(T).Fn",
			FilePath: "main.go", LineStart: 3},
		{ID: "symbol:go:example.com/m:(T).Fn#h.State.write@3", Kind: domain.KindFieldAccess,
			Name: "h.State", FilePath: "main.go", LineStart: 3,
			Properties: map[string]any{"access_kind": "write", "full_path": "example.com/m.T.State",
				"func_id": "symbol:go:example.com/m:(T).Fn", "instance_path": "h.State"}},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "vt-1", ToolName: "test", Status: domain.BuildSuccess}); err != nil {
		t.Fatal(err)
	}
	return dir, action.New(r)
}

func runVT(t *testing.T, acts *action.Actions, dir, id string, timeout time.Duration) (string, string, int) {
	t.Helper()
	var out, errOut string
	code := 0
	errOut = captureStderr(func() {
		out = captureStdout(func() {
			code = queryValueTrace(acts, id, 8, 1.0, false,
				outputOpts{json: true, repoPath: dir}, "", timeout)
		})
	})
	return out, errOut, code
}

func TestValueTraceBudgetStatsAndCache(t *testing.T) {
	dir, acts := seedValueTraceCLI(t)
	id := "symbol:go:example.com/m:(T).Fn#h.State.write@3"
	out, _, code := runVT(t, acts, dir, id, 0)
	if code != 0 {
		t.Fatalf("value-trace exit=%d", code)
	}
	var res struct {
		Analysis struct {
			Status      string  `json:"status"`
			SQLMS       float64 `json:"sql_ms"`
			SQLQueries  int     `json:"sql_queries"`
			SymbolMS    float64 `json:"symbol_ms"`
			CacheHits   int     `json:"cache_hits"`
			CacheMisses int     `json:"cache_misses"`
		} `json:"analysis"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("JSON: %v\n%s", err, out)
	}
	if res.Analysis.SQLQueries == 0 || res.Analysis.CacheMisses != 1 {
		t.Errorf("首次查询应有 SQL 次数 + miss=1: %+v", res.Analysis)
	}
	// 同索引第二次：命中
	out, _, _ = runVT(t, acts, dir, id, 0)
	res = struct {
		Analysis struct {
			Status      string  `json:"status"`
			SQLMS       float64 `json:"sql_ms"`
			SQLQueries  int     `json:"sql_queries"`
			SymbolMS    float64 `json:"symbol_ms"`
			CacheHits   int     `json:"cache_hits"`
			CacheMisses int     `json:"cache_misses"`
		} `json:"analysis"`
	}{}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if res.Analysis.CacheHits != 1 || res.Analysis.CacheMisses != 0 {
		t.Errorf("同索引暖查询应命中缓存: %+v", res.Analysis)
	}

	// 索引身份变化 → 旧缓存不再命中
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlite.NewRepo(db).Save(&domain.BuildMeta{BuildID: "vt-2",
		ToolName: "test", Status: domain.BuildSuccess}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	out, _, _ = runVT(t, acts, dir, id, 0)
	res.Analysis.CacheHits, res.Analysis.CacheMisses = 0, 0
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if res.Analysis.CacheMisses != 1 || res.Analysis.CacheHits != 0 {
		t.Errorf("索引身份变化后旧缓存不得命中: %+v", res.Analysis)
	}

	// 1µs 预算：非零退出 + 明确取消（不返回完整成功）
	_, stderr, code := runVT(t, acts, dir, id, time.Microsecond)
	if code == 0 {
		t.Errorf("预算耗尽应非零退出")
	}
	if !strings.Contains(stderr, "查询预算") && !strings.Contains(stderr, "已取消") {
		t.Errorf("应说明预算/取消: %s", stderr)
	}
}

func TestVTCancelDirect(t *testing.T) {
	dir, acts := seedValueTraceCLI(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Microsecond)
	defer cancel()
	time.Sleep(time.Millisecond)
	_, _, err := acts.ValueTraceWithContext(ctx, "symbol:go:example.com/m:(T).Fn#h.State.write@3", 8, 1.0, false)
	if err == nil {
		t.Fatalf("expired ctx should cancel; dir=%s", dir)
	}
	t.Logf("err=%v", err)
}
