package action

// todo25 §3.4：多条嵌入提升路径的保守反例（writer 分属两个嵌入基类时不得
// 任选一个）。

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

func TestSeqFieldOwnerTypesMultiplePromotionPaths(t *testing.T) {
	src := `package m

type API interface{ Work() }

type A struct{ worker API }
type B struct{ worker API }

func (a *A) SetA(v API) { a.worker = v }
func (b *B) SetB(v API) { b.worker = v }

type Box struct {
	*A
	*B
}
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "flow.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	setA := "symbol:go:example.com/m:(A).SetA"
	setB := "symbol:go:example.com/m:(B).SetB"
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: domain.CanonicalID(setA), Kind: domain.KindMethod, Name: "(A).SetA", FilePath: "flow.go", LineStart: 8},
		{ID: domain.CanonicalID(setB), Kind: domain.KindMethod, Name: "(B).SetB", FilePath: "flow.go", LineStart: 9},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ fn, field string }{{setA, ".A.worker"}, {setB, ".B.worker"}} {
		// P1-11：摘要表 function_id 已是整数代理键 function_ref
		if _, err := db.Exec(`INSERT INTO function_field_summary
			(function_ref, access_kind, field_path, line_start)
			VALUES ((SELECT id_int FROM nodes WHERE id = ?), 'direct_write', ?, 0)`,
			row.fn, row.field); err != nil {
			t.Fatal(err)
		}
	}
	acts := New(r)
	req := CodeSequenceRequest{RepoAbs: dir}
	owners := seqFieldOwnerTypes(acts, req, "flow.go", "Box", "worker")
	if len(owners) != 2 || owners[0] == owners[1] {
		t.Fatalf("两个嵌入基类都有 writer 时应返回两条提升路径（供调用方保守放弃），got %v", owners)
	}
	// writer 方法名不同：即使同名方法不在同一类型，也不能任选一个 owner。
	paramsA := seqFieldWriterParams(acts, req, "A", "worker")
	paramsB := seqFieldWriterParams(acts, req, "B", "worker")
	if _, ok := paramsA["SetA"]; !ok {
		t.Errorf("A.worker writer 应包含 SetA: %v", paramsA)
	}
	if _, ok := paramsB["SetB"]; !ok {
		t.Errorf("B.worker writer 应包含 SetB: %v", paramsB)
	}
}
