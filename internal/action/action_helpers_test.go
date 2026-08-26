package action

// R89 测试：Actions.Helpers 工具函数——游离函数（非方法）且被 ≥N 个
// 包调用；方法不算、同包调用不算新包、低于阈值过滤。

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// SeedHelpersRepo：helper 被 3 个包调用；single 被 1 个包调用；
// method 是方法（不算游离）；unused 无调用。导出——cli 转发测试复用。
func SeedHelpersRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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
	dir := SeedHelpersRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)

	a := New(r)
	// 默认阈值 3：只有 helper（3 包）；single(1) / unused(0) / 方法排除
	out, err := a.Helpers(HelpersRequest{MinPackages: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Name != "helper" {
		t.Fatalf("阈值 3 结果 = %+v; want 仅 helper", out)
	}
	if out[0].Pkgs != 3 || out[0].Callers != 4 {
		t.Errorf("helper pkgs/callers = %d/%d; want 3/4（a 包 2 调用方去重为 1 包）", out[0].Pkgs, out[0].Callers)
	}
	// 阈值 1：helper + single（方法仍排除）
	out, err = a.Helpers(HelpersRequest{MinPackages: 1})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, h := range out {
		names[h.Name] = true
	}
	if !names["helper"] || !names["single"] || names["(T).Method"] || names["unused"] {
		t.Errorf("阈值 1 结果 = %+v; want helper+single（方法/unused 排除）", out)
	}
}
