package cli

// todo10-7：query effects 命令端到端——四组输出（写入/远程/事务/返回）与
// 证据标注；可疑形态必须显式警示。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedEffectsCLI 建 fixture：写字段 + 返回错误 + 事务调用 + 无表列映射。
func seedEffectsCLI(t *testing.T) string {
	t.Helper()
	dir := seedRepo(t)
	src := "package m\n\nfunc Save() error { return nil }\n\nfunc Commit() error {\n\ttx := Begin()\n\t_ = tx\n\treturn nil\n}\n\nfunc Begin() error { return nil }\n"
	if err := os.WriteFile(filepath.Join(dir, "f.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:Commit", Kind: domain.KindFunction, Name: "Commit", FilePath: "f.go", LineStart: 6},
		{ID: "symbol:go:example.com/m:Save", Kind: domain.KindFunction, Name: "Save", FilePath: "f.go", LineStart: 3},
		{ID: "symbol:http:sms.example.com:route./send", Kind: domain.KindHTTPRoute, Name: "route./send",
			FilePath: "f.go", Properties: map[string]any{"path": "/send"}},
	}, []*domain.Fact{
		// Commit → Save（depth 2 展开用）
		{SourceID: "symbol:go:example.com/m:Commit", TargetID: "symbol:go:example.com/m:Save",
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": 7}},
		{SourceID: "symbol:go:example.com/m:Save", TargetID: "symbol:http:sms.example.com:route./send",
			Kind: domain.FactHTTPCall, Confidence: 1.0, Metadata: map[string]any{"host": "sms.example.com", "path": "/send", "line_num": 4}},
	}, []*domain.FunctionFieldSummary{
		{FunctionID: "symbol:go:example.com/m:Commit", AccessKind: domain.SummaryDirectWrite,
			FieldPath: "example.com/m.Ledger.State", LineStart: 8},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "eff1", ToolName: "test", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestQueryEffectsText：四组小节齐全；无表列映射标 unverified；事务只陈述事实。
func TestQueryEffectsText(t *testing.T) {
	dir := seedEffectsCLI(t)
	out := captureStdout(func() {
		if code := cmdQuery([]string{"effects", "symbol:go:example.com/m:Commit", "--repo", dir}); code != 0 {
			t.Errorf("query effects exit = %d", code)
		}
	})
	for _, want := range []string{"副作用与返回", "数据库写入", "远程写", "事务范围", "返回结果",
		"unverified", "no_table_mapping", "Begin@"} {
		if !strings.Contains(out, want) {
			t.Errorf("输出应含 %q:\n%s", want, out)
		}
	}
	// 事务：只陈述观察到的事实，不得下"回滚"结论
	if strings.Contains(out, "全部回滚") && !strings.Contains(out, "不代表") {
		t.Errorf("事务范围不得推断回滚:\n%s", out)
	}
}

// TestQueryEffectsJSON：契约字段齐全（db_writes/remote_writes/tx/result/evidence）。
func TestQueryEffectsJSON(t *testing.T) {
	dir := seedEffectsCLI(t)
	out := captureStdout(func() {
		if code := cmdQuery([]string{"effects", "symbol:go:example.com/m:Commit", "--repo", dir, "--json", "--depth", "2"}); code != 0 {
			t.Errorf("query effects --json exit = %d", code)
		}
	})
	for _, want := range []string{`"db_writes"`, `"remote_writes"`, `"tx"`, `"result"`, `"evidence"`, `"verdict"`, `"suspicious"`} {
		if !strings.Contains(out, want) {
			t.Errorf("JSON 应含 %s:\n%s", want, out)
		}
	}
	// depth 2：被调 Save 的 http 出站应出现
	if !strings.Contains(out, "sms.example.com") {
		t.Errorf("--depth 2 应包含被调函数的出站调用:\n%s", out)
	}
}
