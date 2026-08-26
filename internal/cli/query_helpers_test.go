package cli

// R89 测试：cmdQueryHelpers 转发（业务逻辑在 action——Actions.Helpers
// 已单独测试）；cli 只做配置默认值 + 参数转发 + 输出。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// TestCmdQueryHelpersForward：命令行转发——输出含工具函数（阈值 1）。
func TestCmdQueryHelpersForward(t *testing.T) {
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
		{ID: "symbol:go:example.com/m/a:caller1", Kind: domain.KindFunction, Name: "caller1", FilePath: "a/a.go"},
		{ID: "symbol:go:example.com/m/b:caller2", Kind: domain.KindFunction, Name: "caller2", FilePath: "b/b.go"},
	}, []*domain.Fact{
		{SourceID: "symbol:go:example.com/m/a:caller1", TargetID: "symbol:go:example.com/m/util:helper", Kind: domain.FactCalls, Confidence: 1.0},
		{SourceID: "symbol:go:example.com/m/b:caller2", TargetID: "symbol:go:example.com/m/util:helper", Kind: domain.FactCalls, Confidence: 1.0},
		{SourceID: "symbol:go:example.com/m/a:caller1", TargetID: "symbol:go:example.com/m/util:single", Kind: domain.FactCalls, Confidence: 1.0},
	}, nil); err != nil {
		t.Fatal(err)
	}
	outStr := captureStdout(func() {
		if code := cmdQueryHelpers(r, 1, false); code != 0 {
			t.Fatalf("cmdQueryHelpers exit = %d", code)
		}
	})
	for _, want := range []string{"helper", "single"} {
		if !strings.Contains(outStr, want) {
			t.Errorf("命令输出应含 %s（阈值 1）:\n%s", want, outStr)
		}
	}
}

// TestHelperMinPackagesConfig：配置 helpers.min_packages 生效（默认 3）。
func TestHelperMinPackagesConfig(t *testing.T) {
	if got := helperMinPackages(); got != 3 {
		t.Errorf("默认 min_packages = %d; want 3", got)
	}
}
