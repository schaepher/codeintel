package cli

// todo10-6：query async 命令端到端——文本列角色/关联/证据，JSON 契约；
// "仅同作用域"必须显式标注不构成因果。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedAsyncCLI 建三入口 fixture（人工审核 / 定时任务 / 无关同包函数）。
func seedAsyncCLI(t *testing.T) string {
	t.Helper()
	dir := seedRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:Approve", Kind: domain.KindFunction, Name: "Approve", FilePath: "f.go", LineStart: 5},
		{ID: "symbol:go:example.com/m:Sweep", Kind: domain.KindFunction, Name: "Sweep", FilePath: "f.go", LineStart: 9},
		{ID: "symbol:go:example.com/m:Unrelated", Kind: domain.KindFunction, Name: "Unrelated", FilePath: "f.go", LineStart: 13},
	}
	sums := []*domain.FunctionFieldSummary{
		{FunctionID: "symbol:go:example.com/m:Approve", AccessKind: domain.SummaryDirectWrite,
			FieldPath: "example.com/m.Order.State", LineStart: 6},
		{FunctionID: "symbol:go:example.com/m:Sweep", AccessKind: domain.SummaryDirectRead,
			FieldPath: "example.com/m.Order.State", LineStart: 11},
		// 同一作用域但字段不同：只共享"表/类型"——不得当成因果
		{FunctionID: "symbol:go:example.com/m:Unrelated", AccessKind: domain.SummaryDirectWrite,
			FieldPath: "example.com/m.Order.Note", LineStart: 14},
	}
	if _, err := r.SaveBatchStats(nodes, nil, sums); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "async1", ToolName: "test", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestQueryAsyncText：文本输出角色与关联证据；无关同包函数标"不构成因果"。
func TestQueryAsyncText(t *testing.T) {
	dir := seedAsyncCLI(t)
	out := captureStdout(func() {
		if code := cmdQuery([]string{"async", "symbol:go:example.com/m:Approve", "--repo", dir}); code != 0 {
			t.Errorf("query async exit = %d", code)
		}
	})
	for _, want := range []string{"异步生命周期", "human", "cron", "关联（证据）", "field:example.com/m.Order.State", "corroborated"} {
		if !strings.Contains(out, want) {
			t.Errorf("输出应含 %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "不构成因果") {
		t.Errorf("仅同包作用域的关联必须显式标注不构成因果:\n%s", out)
	}
}

// TestQueryAsyncJSON：契约字段（nodes/links/diagnostics + verdict/reason）。
func TestQueryAsyncJSON(t *testing.T) {
	dir := seedAsyncCLI(t)
	out := captureStdout(func() {
		if code := cmdQuery([]string{"async", "symbol:go:example.com/m:Approve", "--repo", dir, "--json"}); code != 0 {
			t.Errorf("query async --json exit = %d", code)
		}
	})
	for _, want := range []string{`"seed"`, `"nodes"`, `"links"`, `"role"`, `"reason"`, `"verdict"`} {
		if !strings.Contains(out, want) {
			t.Errorf("JSON 应含 %s:\n%s", want, out)
		}
	}
}
