package action

// todo12-8 R2：写点区分字段赋值 / 数据库写调用 / 已验证提交范围。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

const writeKindSrc = `package m

type Order struct{ State string }
type Tx struct{}

func (t *Tx) Begin() {}
func (t *Tx) Commit() {}

func Save(o *Order, tx *Tx) {
	tx.Begin()
	o.State = "done"
	tx.Commit()
}

func SaveOutside(o *Order, tx *Tx) {
	o.State = "early"
	tx.Begin()
	tx.Commit()
}
`

func TestEffectsWriteKindAndCommitScope(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "flow.go"), []byte(writeKindSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	id := func(n string) domain.CanonicalID { return domain.CanonicalID("symbol:go:example.com/m:" + n) }
	nodes := []*domain.CodeEntity{
		{ID: id("Save"), Kind: domain.KindFunction, Name: "Save", FilePath: "flow.go", LineStart: 9},
		{ID: id("SaveOutside"), Kind: domain.KindFunction, Name: "SaveOutside", FilePath: "flow.go", LineStart: 15},
		// 外部表列（db_write 精确映射依据）
		{ID: "symbol:external:orders.state", Kind: domain.KindFieldAccess, Name: "orders.state",
			Properties: map[string]any{"is_external": "true", "type_string": "gorm", "access_kind": "write"}},
	}
	fields := []*domain.FunctionFieldSummary{
		// ORM 写：field_path 就是 table.column → db_write
		{FunctionID: id("Save"), AccessKind: domain.SummaryDirectWrite, FieldPath: "orders.state", LineStart: 11},
		// 结构体字段写：类型限定路径 → field_write（避免按列名误判）
		{FunctionID: id("Save"), AccessKind: domain.SummaryDirectWrite,
			FieldPath: "example.com/m.Order.State", LineStart: 11},
		{FunctionID: id("SaveOutside"), AccessKind: domain.SummaryDirectWrite,
			FieldPath: "example.com/m.Order.State", LineStart: 16},
	}
	if _, err := r.SaveBatchStats(nodes, nil, fields); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "writekind", ToolName: "test", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	acts := New(r)

	res, err := acts.Effects(EffectsRequest{Target: string(id("Save")), RepoAbs: dir, Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	var dbWrite, fieldWrite *DBEffect
	for i := range res.DBWrites {
		switch res.DBWrites[i].Kind {
		case "db_write":
			dbWrite = &res.DBWrites[i]
		case "field_write":
			fieldWrite = &res.DBWrites[i]
		}
	}
	if dbWrite == nil || dbWrite.Table != "orders" || dbWrite.Column != "state" {
		t.Errorf("ORM 写应标 db_write + 表列: %+v", res.DBWrites)
	}
	if fieldWrite == nil || fieldWrite.Table != "" {
		t.Errorf("结构体字段写应标 field_write 且不冒充表列: %+v", res.DBWrites)
	}
	// 事务范围内的写点：begin_commit
	if len(res.Paths) != 1 || len(res.Paths[0].Writes) != 1 {
		t.Fatalf("Save 应一条路径一个写点: %+v", res.Paths)
	}
	if k := res.Paths[0].Writes[0].Kind; k != "field_assign" {
		t.Errorf("AST 字段赋值 kind = %q; want field_assign", k)
	}
	if s := res.Paths[0].Writes[0].CommitScope; s != "begin_commit" {
		t.Errorf("事务内写点 commit_scope = %q; want begin_commit", s)
	}
	for _, w := range res.DBWrites {
		if w.CommitScope != "begin_commit" {
			t.Errorf("函数级写点也应在事务范围内: %+v", w)
		}
	}

	// 事务外写入 → outside_or_unknown（不推断回滚）
	outRes, err := acts.Effects(EffectsRequest{Target: string(id("SaveOutside")), RepoAbs: dir, Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(outRes.Paths) != 1 || len(outRes.Paths[0].Writes) != 1 {
		t.Fatalf("SaveOutside 路径: %+v", outRes.Paths)
	}
	cs := outRes.Paths[0].Writes[0].CommitScope
	if cs == "begin_commit" {
		t.Errorf("事务开始前的写入不得标已提交范围: %+v", outRes.Paths[0].Writes[0])
	}
	if !strings.Contains(cs, "outside") && cs != "none" {
		t.Errorf("commit_scope = %q; want outside_or_unknown/none", cs)
	}
}
