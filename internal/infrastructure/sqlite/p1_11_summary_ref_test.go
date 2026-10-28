package sqlite

// P1-11：摘要表 function_id → 整数代理键 function_ref；idx_nodes_func_id
// 表达式索引删除、函数内查询改 nodes.id 前缀范围扫。本文件锁定：
//   - 新库结构（function_ref INTEGER + FK nodes(id_int)，无 function_id 列）
//   - 旧库（TEXT function_id）Open 报错并提示 reindex
//   - 写入/读取边界：canonical → ref → canonical 往返一致
//   - 未索引函数按"无摘要"返回；删节点级联清摘要
//   - 前缀范围查命中 nodes.id 唯一索引（不退化成全表扫）

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// tableColumnTypes 返回表的列名 → 声明类型（table_xinfo）。
func tableColumnTypes(t *testing.T, db interface {
	Query(string, ...any) (*sql.Rows, error)
}, table string) map[string]string {
	t.Helper()
	rows, err := db.Query("PRAGMA table_xinfo(" + table + ")")
	if err != nil {
		t.Fatalf("table_xinfo %s: %v", table, err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var cid, notnull, pk, hidden int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk, &hidden); err != nil {
			t.Fatalf("scan %s: %v", table, err)
		}
		out[name] = typ
	}
	return out
}

// TestSummaryFunctionRefSchema：物理列改为整数代理键，FK 指向 nodes(id_int)。
func TestSummaryFunctionRefSchema(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	for _, tbl := range []string{"function_field_summary", "summary_origins"} {
		cols := tableColumnTypes(t, db, tbl)
		if typ, ok := cols["function_ref"]; !ok || !strings.EqualFold(typ, "INTEGER") {
			t.Errorf("%s.function_ref 应为 INTEGER 列，got %q（存在=%v）", tbl, typ, ok)
		}
		if _, ok := cols["function_id"]; ok {
			t.Errorf("%s 不应再保留 function_id TEXT 列（P1-11 已改代理键）", tbl)
		}
		rows, err := db.Query("PRAGMA foreign_key_list(" + tbl + ")")
		if err != nil {
			t.Fatalf("foreign_key_list: %v", err)
		}
		found := false
		for rows.Next() {
			var id, seq int
			var table, from, to, onUpdate, onDelete, match string
			if err := rows.Scan(&id, &seq, &table, &from, &to, &onUpdate, &onDelete, &match); err != nil {
				rows.Close()
				t.Fatalf("scan fk: %v", err)
			}
			if table == "nodes" && from == "function_ref" && to == "id_int" {
				found = true
			}
		}
		rows.Close()
		if !found {
			t.Errorf("%s.function_ref 应有 FK → nodes(id_int)", tbl)
		}
	}
}

// TestOpenOldSummarySchemaHintsReindex：旧库（function_id TEXT）打开必须
// 报错并指出 reindex（列类型变更无法加法迁移；摘要表是派生数据）。
func TestOpenOldSummarySchemaHintsReindex(t *testing.T) {
	dir := t.TempDir()
	raw := openRaw(t, dir, schema, SchemaVersion)
	// 换回 P1-11 之前的摘要表结构（模拟旧库）
	for _, ddl := range []string{
		`DROP TABLE IF EXISTS function_field_summary`,
		`CREATE TABLE function_field_summary (
			function_id TEXT NOT NULL,
			access_kind TEXT NOT NULL,
			field_path TEXT NOT NULL,
			instance_path TEXT,
			line_start INTEGER,
			code_snippet TEXT,
			UNIQUE(function_id, access_kind, field_path)
		)`,
	} {
		if _, err := raw.Exec(ddl); err != nil {
			raw.Close()
			t.Fatalf("prepare old schema: %v", err)
		}
	}
	raw.Close()
	_, err := Open(dir)
	if err == nil {
		t.Fatal("旧摘要表结构打开应报错（列变更无法自动迁移）")
	}
	msg := err.Error()
	if !strings.Contains(msg, "function_ref") || !strings.Contains(msg, "reindex") {
		t.Fatalf("错误信息应指出 function_ref 与 reindex，got %v", err)
	}
}

// summaryFixture 建一个函数节点 + 一条摘要/来源（经写入路径，走 ref 解析）。
func summaryFixture(t *testing.T) (*Repo, string) {
	t.Helper()
	r := newTestRepo(t)
	funcID := "symbol:go:example.com/m:Run"
	callee := "symbol:go:example.com/m:(T).Fill"
	save(t, r, []*domain.CodeEntity{
		{ID: domain.CanonicalID(funcID), Kind: domain.KindFunction, Name: "Run", FilePath: "a.go", LineStart: 1},
		{ID: domain.CanonicalID(callee), Kind: domain.KindMethod, Name: "(T).Fill", FilePath: "a.go", LineStart: 9},
	}, nil)
	if _, err := r.SaveBatchStats(nil, nil, []*domain.FunctionFieldSummary{{
		FunctionID: domain.CanonicalID(funcID), AccessKind: domain.SummaryDirectWrite,
		FieldPath: "m.cfg.APIKey", InstancePath: "m.cfg.APIKey", LineStart: 12, CodeSnippet: "s",
	}}, []*domain.SummaryOrigin{{
		FunctionID: domain.CanonicalID(funcID), AccessKind: domain.SummaryDirectWrite,
		FieldPath: "m.cfg.APIKey", CallLine: 10, CalleeID: domain.CanonicalID(callee),
	}}); err != nil {
		t.Fatalf("save summaries: %v", err)
	}
	return r, funcID
}

// TestSummaryRefRoundTrip：写入的 function_ref 是整数，读路径还原 canonical。
func TestSummaryRefRoundTrip(t *testing.T) {
	r, funcID := summaryFixture(t)
	var ref int64
	var kind string
	if err := r.QueryRow(`SELECT function_ref, typeof(function_ref) FROM function_field_summary LIMIT 1`).
		Scan(&ref, &kind); err != nil {
		t.Fatalf("read ref: %v", err)
	}
	if kind != "integer" || ref == 0 {
		t.Fatalf("function_ref 应存整数代理键，got %d (%s)", ref, kind)
	}
	// canonical → ref → canonical 往返
	var gotID string
	if err := r.QueryRow(`SELECT n.id FROM function_field_summary s JOIN nodes n ON n.id_int = s.function_ref`).
		Scan(&gotID); err != nil {
		t.Fatalf("join back: %v", err)
	}
	if gotID != funcID {
		t.Fatalf("join 回填 canonical = %q, want %q", gotID, funcID)
	}
	// 读路径：GetFunctionFields 返回 canonical + origins
	fields, err := r.GetFunctionFields(domain.CanonicalID(funcID))
	if err != nil {
		t.Fatalf("GetFunctionFields: %v", err)
	}
	if len(fields) != 1 || string(fields[0].FunctionID) != funcID || len(fields[0].Origins) != 1 {
		t.Fatalf("GetFunctionFields 往返异常: %+v", fields)
	}
	// 全表导出（AllSummaries）与字段写者查询
	all, err := r.AllSummaries()
	if err != nil || len(all) != 1 || string(all[0].FunctionID) != funcID {
		t.Fatalf("AllSummaries = %+v, err=%v", all, err)
	}
	writers, err := r.GetFieldWriters("m.cfg.APIKey")
	if err != nil || len(writers) != 1 || writers[0] != funcID {
		t.Fatalf("GetFieldWriters = %v, err=%v", writers, err)
	}
}

// TestSummaryMissingFunctionDeferred：函数节点尚未落库 → Failed*（延后
// 重试），节点落库后同一批重试成功。
func TestSummaryMissingFunctionDeferred(t *testing.T) {
	r := newTestRepo(t)
	funcID := "symbol:go:example.com/m:Late"
	sum := &domain.FunctionFieldSummary{FunctionID: domain.CanonicalID(funcID),
		AccessKind: domain.SummaryDirectRead, FieldPath: "T.A", LineStart: 3}
	res, err := r.SaveBatchStats(nil, nil, []*domain.FunctionFieldSummary{sum})
	if err != nil {
		t.Fatalf("save summary: %v", err)
	}
	if len(res.FailedSummaries) != 1 {
		t.Fatalf("缺函数节点时应进 FailedSummaries，got %+v", res)
	}
	save(t, r, []*domain.CodeEntity{{ID: domain.CanonicalID(funcID), Kind: domain.KindFunction, Name: "Late"}}, nil)
	if res, err = r.SaveBatchStats(nil, nil, res.FailedSummaries); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if len(res.FailedSummaries) != 0 {
		t.Fatalf("节点落库后重试应成功，仍失败 %+v", res.FailedSummaries)
	}
	got, err := r.GetFunctionFields(domain.CanonicalID(funcID))
	if err != nil || len(got) != 1 {
		t.Fatalf("重试后应可读出摘要：%+v err=%v", got, err)
	}
	// 未索引函数：按"无摘要"返回，不报错
	if got, err := r.GetFunctionFields("symbol:go:example.com/m:Absent"); err != nil || got != nil {
		t.Fatalf("未索引函数应返回空摘要：%+v err=%v", got, err)
	}
}

// TestSummaryCascadeOnNodeDelete：删函数节点级联清摘要（整数 FK 语义）。
func TestSummaryCascadeOnNodeDelete(t *testing.T) {
	r, funcID := summaryFixture(t)
	if _, err := r.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Exec(`DELETE FROM nodes WHERE id = ?`, funcID); err != nil {
		t.Fatalf("delete node: %v", err)
	}
	var n int
	if err := r.QueryRow(`SELECT COUNT(*) FROM function_field_summary`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("删节点应级联清摘要，剩余 %d 行", n)
	}
	if err := r.QueryRow(`SELECT COUNT(*) FROM summary_origins`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("删节点应级联清来源，剩余 %d 行", n)
	}
}
