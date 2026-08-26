package sqlite

// R85 测试：物理物化（--base 分层）——base 索引数据复制到本地
// （秒级，非分析）；幂等（同一 base commit 跳过）；base commit 变化
// 重新物化；物化后按包增量写变更包。

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// seedMaterializeBase 构造 base 索引（完整数据：节点 + 边 + 摘要）。
func seedMaterializeBase(t *testing.T, commit string) string {
	t.Helper()
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:m:A", Kind: domain.KindFunction, Name: "A", FilePath: "a.go", LineStart: 1},
		{ID: "symbol:go:m:B", Kind: domain.KindFunction, Name: "B", FilePath: "b.go", LineStart: 1},
	}, []*domain.Fact{
		{SourceID: "symbol:go:m:A", TargetID: "symbol:go:m:B", Kind: domain.FactCalls, Confidence: 0.9},
	}, []*domain.FunctionFieldSummary{
		{FunctionID: "symbol:go:m:A", AccessKind: domain.SummaryDirectWrite, FieldPath: "m.T.X", InstancePath: "t.X", LineStart: 3},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "b1", CommitSHA: commit, ToolName: "all", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	return dir
}

// TestMaterializeBaseCopy：物化复制——本地获得 base 全部数据（节点/
// 边/摘要），materialize 记录 commit。
func TestMaterializeBaseCopy(t *testing.T) {
	base := seedMaterializeBase(t, "abc123")
	ws := t.TempDir()
	db, err := Open(ws)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := NewRepo(db)
	done, err := r.MaterializeBase(base)
	if err != nil || !done {
		t.Fatalf("MaterializeBase = %v, %v; want done", done, err)
	}
	// 数据完整（节点/边/摘要）
	if n, err := r.GetSymbol("symbol:go:m:A"); err != nil || n.Name != "A" {
		t.Errorf("节点 A 应物化: %+v, %v", n, err)
	}
	edges, err := r.GetCallees("symbol:go:m:A", 1, 0.8)
	if err != nil || len(edges) != 1 {
		t.Errorf("边应物化: %v, %v", edges, err)
	}
	sums, err := r.GetFunctionFields("symbol:go:m:A")
	if err != nil || len(sums) != 1 {
		t.Errorf("摘要应物化: %v, %v", sums, err)
	}
	// materialize 记录
	var commit string
	if err := r.QueryRow(`SELECT commit_sha FROM build_metadata WHERE tool_name='materialize'`).Scan(&commit); err != nil || commit != "abc123" {
		t.Errorf("materialize 记录 = %q, %v; want abc123", commit, err)
	}
}

// TestMaterializeBaseIdempotent：同一 base commit 再次物化 → 跳过
// （本地数据保留——含后续增量写入）。
func TestMaterializeBaseIdempotent(t *testing.T) {
	base := seedMaterializeBase(t, "abc123")
	ws := t.TempDir()
	db, err := Open(ws)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := NewRepo(db)
	if _, err := r.MaterializeBase(base); err != nil {
		t.Fatal(err)
	}
	// 本地增量写入（变更包）
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:m:C", Kind: domain.KindFunction, Name: "C", FilePath: "c.go", LineStart: 1},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	// 再次物化 → 跳过（C 保留）
	done, err := r.MaterializeBase(base)
	if err != nil || done {
		t.Fatalf("同 commit 应跳过物化: done=%v, %v", done, err)
	}
	if n, err := r.GetSymbol("symbol:go:m:C"); err != nil || n.Name != "C" {
		t.Errorf("跳过物化后本地增量应保留: %+v, %v", n, err)
	}
	if n, err := r.GetSymbol("symbol:go:m:A"); err != nil || n.Name != "A" {
		t.Errorf("base 数据应保留: %+v, %v", n, err)
	}
}

// TestMaterializeBaseRecommit：base commit 变化 → 重新物化（清空 +
// 复制新 base；本地旧增量被清除）。
func TestMaterializeBaseRecommit(t *testing.T) {
	base1 := seedMaterializeBase(t, "abc123")
	ws := t.TempDir()
	db, err := Open(ws)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := NewRepo(db)
	if _, err := r.MaterializeBase(base1); err != nil {
		t.Fatal(err)
	}
	// base 更新（新 commit + 新节点）
	base2 := seedMaterializeBase(t, "def456")
	db2, err := Open(base2)
	if err != nil {
		t.Fatal(err)
	}
	r2 := NewRepo(db2)
	if _, err := r2.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:m:D", Kind: domain.KindFunction, Name: "D", FilePath: "d.go", LineStart: 1},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := r2.Save(&domain.BuildMeta{BuildID: "b2", CommitSHA: "def456", ToolName: "all", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	db2.Close()
	// 重新物化 → 新数据
	done, err := r.MaterializeBase(base2)
	if err != nil || !done {
		t.Fatalf("base commit 变化应重新物化: done=%v, %v", done, err)
	}
	if n, err := r.GetSymbol("symbol:go:m:D"); err != nil || n.Name != "D" {
		t.Errorf("新 base 节点应物化: %+v, %v", n, err)
	}
}
