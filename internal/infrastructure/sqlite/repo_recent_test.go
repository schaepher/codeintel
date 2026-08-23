package sqlite

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestRecentChanges：#237 最近变更——commit 按 date 降序、MODIFIED_BY
// 文件聚合、文件内顶层符号（≤5）。
func TestRecentChanges(t *testing.T) {
	repo := newTestRepo(t)
	commit1 := &domain.CodeEntity{ID: "commit:aaa111", Kind: domain.KindCommit, Name: "aaa111aaaaaa",
		Properties: map[string]any{"date": "2026-08-22", "message": "fix: first"}}
	commit2 := &domain.CodeEntity{ID: "commit:bbb222", Kind: domain.KindCommit, Name: "bbb222bbbbbb",
		Properties: map[string]any{"date": "2026-08-23", "message": "feat: second"}}
	fileA := &domain.CodeEntity{ID: "file:a.go", Kind: domain.KindFile, Name: "a.go"}
	fn := &domain.CodeEntity{ID: "symbol:go:example.com/m:Run", Kind: domain.KindFunction, Name: "Run", FilePath: "a.go", LineStart: 3}
	st := &domain.CodeEntity{ID: "symbol:go:example.com/m:Svc", Kind: domain.KindStruct, Name: "Svc", FilePath: "a.go", LineStart: 10}
	// 内部节点（field_access）不应出现在符号列表
	fa := &domain.CodeEntity{ID: "symbol:go:example.com/m:Run#x.read@5", Kind: "field_access", Name: "x", FilePath: "a.go", LineStart: 5}
	if _, err := repo.SaveBatchStats([]*domain.CodeEntity{commit1, commit2, fileA, fn, st, fa}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SaveBatchStats(nil, []*domain.Fact{
		{SourceID: "file:a.go", TargetID: "commit:aaa111", Kind: domain.FactModifiedBy, ToolSource: domain.ToolGit, Confidence: 1.0},
		{SourceID: "file:a.go", TargetID: "commit:bbb222", Kind: domain.FactModifiedBy, ToolSource: domain.ToolGit, Confidence: 1.0},
	}, nil); err != nil {
		t.Fatal(err)
	}

	got, err := repo.RecentChanges(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("变更数 = %d, want 2", len(got))
	}
	// 时间降序：bbb222（08-23）在前
	if got[0].CommitSHA != "commit:bbb222" || got[1].CommitSHA != "commit:aaa111" {
		t.Errorf("应按 date 降序: %s, %s", got[0].CommitSHA, got[1].CommitSHA)
	}
	if got[0].Message != "feat: second" {
		t.Errorf("message = %q, want feat: second", got[0].Message)
	}
	// 文件聚合 + 符号（排除 field_access）
	if len(got[0].Files) != 1 || got[0].Files[0].Path != "a.go" {
		t.Fatalf("文件列表 = %+v", got[0].Files)
	}
	syms := got[0].Files[0].Symbols
	if len(syms) != 2 {
		t.Fatalf("符号数 = %d, want 2（Run + Svc，排除 field_access）: %+v", len(syms), syms)
	}
	if syms[0].Name != "Run" || syms[1].Name != "Svc" {
		t.Errorf("符号顺序/内容 = %+v", syms)
	}
	if syms[0].Line != 3 {
		t.Errorf("Run line = %d, want 3", syms[0].Line)
	}
	// limit 生效
	got1, err := repo.RecentChanges(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got1) != 1 || got1[0].CommitSHA != "commit:bbb222" {
		t.Errorf("limit=1 应返回最新一条, got %+v", got1)
	}
}
