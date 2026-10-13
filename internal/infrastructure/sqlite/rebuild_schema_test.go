package sqlite

import (
	"errors"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestRebuildDerivedSchemaPreservesManualData：旧 schema 重建（事务重置
// 派生图表）保留人工数据 relation_rules / qa_history，派生节点清空，
// 且之后 Open 正常（schema 版本已刷新）。
func TestRebuildDerivedSchemaPreservesManualData(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	repo := NewRepo(db)
	if _, err := repo.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:main", Kind: domain.KindFunction, Name: "main", FilePath: "main.go"},
	}, nil, nil); err != nil {
		t.Fatalf("save node: %v", err)
	}
	if _, err := repo.AddRelationRule(domain.RelationRule{FromTable: "a", FromCol: "aid", ToTable: "b", ToCol: "id"}); err != nil {
		t.Fatalf("add rule: %v", err)
	}
	if err := repo.SaveQA(&domain.QARecord{Question: "q1", Answer: "a1"}); err != nil {
		t.Fatalf("save qa: %v", err)
	}
	db.Close()

	if err := RebuildDerivedSchema(dir); err != nil {
		t.Fatalf("RebuildDerivedSchema: %v", err)
	}
	db2, err := Open(dir)
	if err != nil {
		t.Fatalf("Open after rebuild: %v", err)
	}
	defer db2.Close()
	repo2 := NewRepo(db2)
	if _, err := repo2.GetSymbol("symbol:go:example.com/m:main"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("派生节点应已清空，got err=%v", err)
	}
	rules, err := repo2.ListRelationRules()
	if err != nil || len(rules) != 1 {
		t.Errorf("人工规则应保留，rules=%v err=%v", rules, err)
	}
	var qaCount int
	if err := db2.QueryRow("SELECT COUNT(*) FROM qa_history").Scan(&qaCount); err != nil || qaCount != 1 {
		t.Errorf("问答应保留，count=%d err=%v", qaCount, err)
	}
}

// TestRebuildDerivedSchemaRollbackOnFailure：重置失败回滚——DDL 失败时
// 数据库保持原样（原节点仍在），不删除文件。
func TestRebuildDerivedSchemaRollbackOnFailure(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	repo := NewRepo(db)
	if _, err := repo.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:keep", Kind: domain.KindFunction, Name: "keep", FilePath: "keep.go"},
	}, nil, nil); err != nil {
		t.Fatalf("save node: %v", err)
	}
	db.Close()

	old := rebuildDDL
	rebuildDDL = "THIS IS NOT VALID SQL"
	defer func() { rebuildDDL = old }()
	if err := RebuildDerivedSchema(dir); err == nil {
		t.Fatal("非法 DDL 应导致重建失败")
	}
	db2, err := Open(dir)
	if err != nil {
		t.Fatalf("回滚后 Open 应成功: %v", err)
	}
	defer db2.Close()
	if _, err := NewRepo(db2).GetSymbol("symbol:go:example.com/m:keep"); err != nil {
		t.Errorf("回滚后原节点应仍在，got err=%v", err)
	}
}
