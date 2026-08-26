package action

// R9x 测试（迁自 cli/output_test.go / cli_misc_test.go 的逻辑断言
// 部分）：ExportGraph——callees/value-trace/lifecycle/modules 各型
// 图数据获取分发（渲染留 cli）。

import "testing"

// TestExportGraphCallees：callees 型返回一级被调 facts（深度 1）。
func TestExportGraphCallees(t *testing.T) {
	a, _ := seedRepo(t)
	res, err := a.ExportGraph(ExportGraphRequest{Type: "callees", Target: "symbol:go:example.com/m:main"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Type != "callees" {
		t.Errorf("type = %q", res.Type)
	}
	if len(res.Facts) != 2 {
		t.Errorf("callees facts = %d; want 2（(Svc).Run + helper）", len(res.Facts))
	}
}

// TestExportGraphValueTrace：value-trace 型返回全链行（深度 8）。
func TestExportGraphValueTrace(t *testing.T) {
	a, _ := seedRepo(t)
	res, err := a.ExportGraph(ExportGraphRequest{Type: "value-trace", Target: "symbol:go:example.com/m:main#t0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) == 0 {
		t.Fatal("value-trace rows 为空")
	}
}

// TestExportGraphLifecycle：lifecycle 型解析锚点（名称 → canonical
// ID）+ 全链行 + 条件标注。
func TestExportGraphLifecycle(t *testing.T) {
	a, _ := seedRepo(t)
	res, err := a.ExportGraph(ExportGraphRequest{Type: "lifecycle", Target: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Anchor == "" {
		t.Error("lifecycle anchor 未解析")
	}
}

// TestExportGraphModules：modules 型无需 target——无 grpc 调用时
// 空结果不报错。
func TestExportGraphModules(t *testing.T) {
	a, _ := seedRepo(t)
	res, err := a.ExportGraph(ExportGraphRequest{Type: "modules"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Calls == nil {
		t.Error("modules calls 应为非 nil（可空）")
	}
}
