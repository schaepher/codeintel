package cli

// R88 测试：query helpers 工具函数——游离函数（非方法）且被 ≥N 个包
// 调用（N 默认 3，config.yaml helpers.min_packages 可调）；方法不算、
// 同包调用不算新包、低于阈值过滤。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedHelpersRepo：helper 被 3 个包调用；single 被 1 个包调用；
// method 是方法（不算游离）；unused 无调用。
func seedHelpersRepo(t *testing.T) string {
	t.Helper()
	dir := seedRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m/util:helper", Kind: domain.KindFunction, Name: "helper", FilePath: "util/util.go"},
		{ID: "symbol:go:example.com/m/util:single", Kind: domain.KindFunction, Name: "single", FilePath: "util/util.go"},
		{ID: "symbol:go:example.com/m/util:unused", Kind: domain.KindFunction, Name: "unused", FilePath: "util/util.go"},
		// 方法（kind=method）——不算游离函数
		{ID: "symbol:go:example.com/m/util:(T).Method", Kind: domain.KindMethod, Name: "(T).Method", FilePath: "util/util.go"},
		// 调用方（3 个不同包）
		{ID: "symbol:go:example.com/m/a:caller1", Kind: domain.KindFunction, Name: "caller1", FilePath: "a/a.go"},
		{ID: "symbol:go:example.com/m/b:caller2", Kind: domain.KindFunction, Name: "caller2", FilePath: "b/b.go"},
		{ID: "symbol:go:example.com/m/c:caller3", Kind: domain.KindFunction, Name: "caller3", FilePath: "c/c.go"},
		{ID: "symbol:go:example.com/m/a:caller4", Kind: domain.KindFunction, Name: "caller4", FilePath: "a/a.go"},
	}, []*domain.Fact{
		// helper 被 3 包调用（a 包 2 个调用方 → 去重 1 包）
		{SourceID: "symbol:go:example.com/m/a:caller1", TargetID: "symbol:go:example.com/m/util:helper", Kind: domain.FactCalls, Confidence: 1.0},
		{SourceID: "symbol:go:example.com/m/a:caller4", TargetID: "symbol:go:example.com/m/util:helper", Kind: domain.FactCalls, Confidence: 1.0},
		{SourceID: "symbol:go:example.com/m/b:caller2", TargetID: "symbol:go:example.com/m/util:helper", Kind: domain.FactCalls, Confidence: 1.0},
		{SourceID: "symbol:go:example.com/m/c:caller3", TargetID: "symbol:go:example.com/m/util:helper", Kind: domain.FactCalls, Confidence: 1.0},
		// single 被 1 包调用
		{SourceID: "symbol:go:example.com/m/a:caller1", TargetID: "symbol:go:example.com/m/util:single", Kind: domain.FactCalls, Confidence: 1.0},
		// 方法被调用——不算游离
		{SourceID: "symbol:go:example.com/m/a:caller1", TargetID: "symbol:go:example.com/m/util:(T).Method", Kind: domain.FactCalls, Confidence: 1.0},
	}, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestQueryHelpers：游离函数 + 跨包去重计数 + 阈值过滤（默认 3）。
func TestQueryHelpers(t *testing.T) {
	dir := seedHelpersRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)

	// 默认阈值 3：只有 helper（3 包）；single(1) / unused(0) / 方法排除
	out := queryHelpers(r, 3)
	if len(out) != 1 || out[0].Name != "helper" {
		t.Fatalf("阈值 3 结果 = %+v; want 仅 helper", out)
	}
	if out[0].Pkgs != 3 || out[0].Callers != 4 {
		t.Errorf("helper pkgs/callers = %d/%d; want 3/4（a 包 2 调用方去重为 1 包）", out[0].Pkgs, out[0].Callers)
	}
	// 阈值 1：helper + single（方法仍排除）
	out = queryHelpers(r, 1)
	names := map[string]bool{}
	for _, h := range out {
		names[h.Name] = true
	}
	if !names["helper"] || !names["single"] || names["(T).Method"] || names["unused"] {
		t.Errorf("阈值 1 结果 = %+v; want helper+single（方法/unused 排除）", out)
	}
	// 命令端到端（--min-packages 1）
	outStr := captureStdout(func() {
		if code := cmdQueryHelpers(r, 1, false); code != 0 {
			t.Fatalf("cmdQueryHelpers exit = %d", code)
		}
	})
	if !strings.Contains(outStr, "single") {
		t.Errorf("命令输出应含 single（阈值 1）:\n%s", outStr)
	}
}

// TestHelperMinPackagesConfig：配置 helpers.min_packages 生效（默认 3）。
func TestHelperMinPackagesConfig(t *testing.T) {
	if got := helperMinPackages(); got != 3 {
		t.Errorf("默认 min_packages = %d; want 3", got)
	}
}
