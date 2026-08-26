package cli

// R77 测试：query processes——系统流程（main 入口聚合；http/grpc
// 入口数据函数与 wiki 流程页同源——wiki_processes_routes 测试覆盖）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedProcessesFixture seedWikiRepo + main.go 实体文件（entrySymbols 的
// R39 噪音过滤要求入口文件存在于仓库——索引快照 + 源码一致性校验）。
func seedProcessesFixture(t *testing.T) string {
	t.Helper()
	dir := seedWikiRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestQueryProcessesCmd：main 入口 + 调用链（接口具体化后实现实体）。
func TestQueryProcessesCmd(t *testing.T) {
	dir := seedProcessesFixture(t)
	out := captureStdout(func() {
		if code := cmdQuery([]string{"processes", "--repo", dir}); code != 0 {
			t.Fatalf("processes exit = %d", code)
		}
	})
	for _, want := range []string{"[main]", "main", "(Svc).Run"} {
		if !strings.Contains(out, want) {
			t.Errorf("processes 应含 %q:\n%s", want, out)
		}
	}
}

// TestQueryProcessesChain：main 的一级调用链展开（(Svc).Run 入口出现）。
func TestQueryProcessesChain(t *testing.T) {
	dir := seedProcessesFixture(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	// (Svc).Run 调 helper——链有真实步骤（端点须先存在——外键约束
	// 静默丢弃缺失端点的边）
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m/svc:helper", Kind: domain.KindFunction, Name: "helper", FilePath: "svc/svc.go", LineStart: 20},
	}, []*domain.Fact{
		{SourceID: "symbol:go:example.com/m/svc:(Svc).Run", TargetID: "symbol:go:example.com/m/svc:helper", Kind: domain.FactCalls, Confidence: 0.9},
	}, nil); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(func() {
		if code := cmdQuery([]string{"processes", "--repo", dir}); code != 0 {
			t.Fatalf("processes exit = %d", code)
		}
	})
	for _, want := range []string{"入口：(Svc).Run", "→"} {
		if !strings.Contains(out, want) {
			t.Errorf("processes 链应含 %q:\n%s", want, out)
		}
	}
}

// TestQueryChainKeyFlows：R78——链上符号关键数据流（入口自身字段读写
// 收集；无出边也收集入口）。
func TestQueryChainKeyFlows(t *testing.T) {
	dir := seedFieldTrace(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	acts := action.New(sqlite.NewRepo(db))
	chain := acts.QueryChain("symbol:go:example.com/m:main")
	if chain == nil {
		t.Fatal("queryChain 返回 nil")
	}
	if len(chain.KeyFlows) == 0 {
		t.Fatalf("链上符号应有字段读写数据流（main 写/读 T.A）")
	}
	if !strings.Contains(chain.KeyFlows[0].Writes[0], "T.A") {
		t.Errorf("KeyFlows 应含 T.A 写入: %v", chain.KeyFlows[0])
	}
}

// TestQueryProcessesKeyFlows：processes 输出含关键数据流小节（value-trace 串联）。
func TestQueryProcessesKeyFlows(t *testing.T) {
	dir := seedFieldTrace(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	// main 出边（链有真实步骤——(Svc).Run 节点须存在）；(Svc).Run 自身
	// 字段读写（链上符号关键数据流——T.B 读）
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m/svc:(Svc).Run", Kind: domain.KindMethod,
			Name: "(Svc).Run", FilePath: "svc/svc.go", LineStart: 5},
	}, []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:main", TargetID: "symbol:go:example.com/m/svc:(Svc).Run",
			Kind: domain.FactCalls, Confidence: 0.9},
	}, []*domain.FunctionFieldSummary{
		{FunctionID: "symbol:go:example.com/m/svc:(Svc).Run", AccessKind: domain.SummaryDirectRead,
			FieldPath: "example.com/m/svc.T.B", InstancePath: "t.B", LineStart: 7, CodeSnippet: "return t.B"},
	}); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(func() {
		if code := cmdQuery([]string{"processes", "--repo", dir}); code != 0 {
			t.Fatalf("processes exit = %d", code)
		}
	})
	for _, want := range []string{"关键数据流", "(Svc).Run", "T.B"} {
		if !strings.Contains(out, want) {
			t.Errorf("processes 应含 %q（关键数据流）:\n%s", want, out)
		}
	}
}

// TestQueryProcessesJSON：--json 结构化（entries + kind）。
func TestQueryProcessesJSON(t *testing.T) {
	dir := seedProcessesFixture(t)
	out := captureStdout(func() {
		if code := cmdQuery([]string{"processes", "--repo", dir, "--json"}); code != 0 {
			t.Fatalf("processes --json exit = %d", code)
		}
	})
	for _, want := range []string{`"entries"`, `"kind"`, "main"} {
		if !strings.Contains(out, want) {
			t.Errorf("processes --json 应含 %q:\n%s", want, out)
		}
	}
}
