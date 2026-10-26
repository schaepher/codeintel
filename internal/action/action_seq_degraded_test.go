package action

// todo13-13：构建降级的具体范围随查询携带（不止全仓计数）。
//   - 失败包/文件/错误位置进 coverage.error_packages + degraded_package 诊断
//   - 目标包在/不在失败列表分别给出 target_coverage 结论（隔离不绝对）
//   - 纯 SQL 降级单独诊断（不冒充包失败）

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

func TestSeqCoverageDiagnostics(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/app\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "good.go"), []byte("package app\n\nfunc Run() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/app:Run", Kind: domain.KindFunction, Name: "Run",
			FilePath: "good.go", LineStart: 3},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	stats := `{"pkg_total":2,"pkg_with_errors":1,"typecheck_errors":1,
		"error_packages":[{"path":"example.com/app/bad","errors":["bad/bad.go:3:18: type error"]}],
		"sql_ast_fail":2,"sql_ast_ok":5,"sql_heuristic":1}`
	if err := r.Save(&domain.BuildMeta{BuildID: "deg", ToolName: "test",
		Status: domain.BuildDegraded, ErrorMsg: "ssa: 1 package with errors",
		DegradeStats: stats}); err != nil {
		t.Fatal(err)
	}
	acts := New(r)
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "example.com/app:Run", RepoAbs: dir, Depth: 1})
	if err != nil || root == nil {
		t.Fatalf("CodeSequence: %v", err)
	}
	byCode := map[string]SeqDiagCount{}
	for _, d := range root.Meta.Diagnostics {
		byCode[d.Code] = d
	}
	d, ok := byCode[SeqDegradedPackage]
	if !ok || !strings.Contains(d.Detail, "example.com/app/bad") ||
		!strings.Contains(d.Detail, "bad/bad.go:3:18") {
		t.Errorf("应带失败包/文件/错误位置: %+v", byCode)
	}
	tc, ok := byCode[SeqTargetCoverage]
	if !ok || !strings.Contains(tc.Detail, "不在失败包列表") ||
		!strings.Contains(tc.Detail, "无法完全证明隔离") {
		t.Errorf("目标不在失败列表时要说明按包隔离且不绝对: %+v", tc)
	}
	sq, ok := byCode[SeqSQLDegraded]
	if !ok || sq.Count != 2 || !strings.Contains(sq.Detail, "SQL AST") {
		t.Errorf("纯 SQL 降级应单独诊断: %+v", sq)
	}
	if root.Meta.Coverage == nil || len(root.Meta.Coverage.ErrorPackages) != 1 ||
		root.Meta.Coverage.ErrorPackages[0].Path != "example.com/app/bad" {
		t.Errorf("coverage 应带结构化失败包: %+v", root.Meta.Coverage)
	}

	// 目标在失败包列表 → 明确不完整
	stats2 := `{"pkg_with_errors":1,"typecheck_errors":1,
		"error_packages":[{"path":"example.com/app","errors":["good.go:3:1: broken"]}]}`
	if err := r.Save(&domain.BuildMeta{BuildID: "deg2", ToolName: "test",
		Status: domain.BuildDegraded, DegradeStats: stats2}); err != nil {
		t.Fatal(err)
	}
	root, err = New(r).CodeSequence(CodeSequenceRequest{
		Target: "example.com/app:Run", RepoAbs: dir, Depth: 1})
	if err != nil || root == nil {
		t.Fatalf("CodeSequence2: %v", err)
	}
	found := false
	for _, d := range root.Meta.Diagnostics {
		if d.Code == SeqTargetCoverage && strings.Contains(d.Detail, "在失败包列表") &&
			strings.Contains(d.Detail, "不完整") {
			found = true
		}
	}
	if !found {
		t.Errorf("目标在失败包列表时应标覆盖不完整: %+v", root.Meta.Diagnostics)
	}
}
