package ssa

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestParseSQLStmt：⑬ 猎 bug——SQL 语句启发式解析形态矩阵
// （parseSQLStmt 此前无直接单测；曾有切片 panic 历史）。
func TestParseSQLStmt(t *testing.T) {
	cases := []struct {
		sql       string
		table     string
		cols      []string
		whereCols []string
	}{
		{"INSERT INTO users(name, email) VALUES(?, ?)", "users", []string{"name", "email"}, nil},
		{"INSERT INTO users VALUES(?, ?)", "users", nil, nil},
		{"INSERT INTO `users`(`name`) VALUES(?)", "users", []string{"name"}, nil},
		{"insert into users (name) values (?)", "users", []string{"name"}, nil},
		{"UPDATE users SET name=?, email=? WHERE id = ?", "users", []string{"name", "email"}, []string{"id"}},
		{"UPDATE users SET name = ?", "users", []string{"name"}, nil},
		{"DELETE FROM users WHERE id = ?", "users", nil, []string{"id"}},
		{"SELECT name FROM users WHERE id = ?", "users", []string{"name"}, []string{"id"}},
		{"SELECT u.name FROM users u JOIN orders o ON u.id = o.uid", "users", []string{"name"}, nil},
		{"SELECT * FROM users", "users", nil, nil},

		{"SELECT x FROM table_a WHERE id = ?", "table_a", []string{"x"}, []string{"id"}},
		// Q250：UPDATE 子串误命中——updated_at 含 "UPDATE"，SELECT 列/
		// DDL 里的 updated_at 不得切出假表 d_at
		{"SELECT updated_at FROM relation_progress WHERE build_id = ?", "relation_progress", []string{"updated_at"}, []string{"build_id"}},
		{"UPDATE users SET updated_at = ? WHERE id = ?", "users", []string{"updated_at"}, []string{"id"}},
		{"CREATE TABLE IF NOT EXISTS relation_progress (updated_at INTEGER)", "", nil, nil},
		// Q250：SELECT 列段——DISTINCT 前缀剥离 + 括号内逗号不切分
		// （COALESCE 内部逗号）+ 引号/函数残留过滤
		{"SELECT DISTINCT callee_id FROM summary_origins WHERE function_id = ?", "summary_origins", []string{"callee_id"}, []string{"function_id"}},
		{"SELECT COALESCE(commit_sha,''), timestamp FROM build_metadata ORDER BY timestamp DESC, rowid DESC LIMIT 1", "build_metadata", []string{"timestamp"}, nil},
		// Q251：fmt.Sprintf 动态 SQL 的 %s 占位符——`%s = ?` 的 s
		// （% 后标识符片段）不得当列名
		{"SELECT * FROM edges WHERE %s = ? AND kind = 'calls' AND confidence >= ?", "edges", nil, []string{"confidence"}},
		{"SELECT DISTINCT s.id, a.name FROM sys_sub_station s INNER JOIN sys_district a ON a.code = s.city_code", "sys_sub_station", []string{"id", "name"}, nil},
		// Q250：多行 SQL（\n\t\tFROM）——FROM 前是 tab/换行非空格，
		// " FROM " 子串匹配不到
		{"SELECT function_id, access_kind, field_path\n\t\tFROM function_field_summary ORDER BY field_path", "function_field_summary", []string{"function_id", "access_kind", "field_path"}, nil},
		{"SELECT *\nFROM nodes", "nodes", nil, nil},
		// Q250：SQLite INSERT OR REPLACE / REPLACE INTO 形态（repo_write 批量写）
		{"INSERT OR REPLACE INTO function_field_summary(function_id, access_kind) VALUES(?, ?)", "function_field_summary", []string{"function_id", "access_kind"}, nil},
		{"REPLACE INTO users(name) VALUES(?)", "users", []string{"name"}, nil},
		{"SELECT * FROM table_b WHERE y = ?", "table_b", nil, []string{"y"}},
		{"SELECT * FROM table_b WHERE a.y = ? AND z = ?", "table_b", nil, []string{"y", "z"}},
		{"SELECT * FROM t WHERE id = ? ORDER BY id", "t", nil, []string{"id"}},
		{"UPDATE users SET name = ? WHERE id = ?", "users", []string{"name"}, []string{"id"}},
		// 子查询作用域（P2a）：EXISTS / IN / = (SELECT…) 内部的列 = ?
		// 属于子查询作用域，不得混入外层过滤列（AST 路径 Walk 递归 +
		// 启发式正则全文匹配都会泄漏）
		{"SELECT * FROM table_a WHERE x = ? AND EXISTS (SELECT 1 FROM table_b WHERE y = ?)", "table_a", nil, []string{"x"}},
		{"SELECT * FROM table_a WHERE id IN (SELECT a_id FROM table_b WHERE y = ?)", "table_a", nil, nil},
		{"SELECT * FROM table_a WHERE x = ? AND y = (SELECT MAX(y) FROM table_b WHERE z = ?)", "table_a", nil, []string{"x"}},
		// 启发式路径（vitess 解析失败降级——%s 残留）：EXISTS 子查询
		// 内部列同样不得泄漏
		{"SELECT * FROM table_a WHERE %s = ? AND EXISTS (SELECT 1 FROM table_b WHERE y = ?)", "table_a", nil, nil},
		{"not sql at all", "", nil, nil},
		{"", "", nil, nil},
	}
	for _, c := range cases {
		table, _, cols, whereCols, _ := parseSQLStmt(c.sql)
		if table != c.table {
			t.Errorf("parseSQLStmt(%q) table = %q, want %q", c.sql, table, c.table)
		}
		if len(cols) != len(c.cols) {
			t.Errorf("parseSQLStmt(%q) cols = %v, want %v", c.sql, cols, c.cols)
			continue
		}
		for i := range cols {
			if cols[i] != c.cols[i] {
				t.Errorf("parseSQLStmt(%q) cols[%d] = %q, want %q", c.sql, i, cols[i], c.cols[i])
			}
		}
		if len(whereCols) != len(c.whereCols) {
			t.Errorf("parseSQLStmt(%q) whereCols = %v, want %v", c.sql, whereCols, c.whereCols)
			continue
		}
		for i := range whereCols {
			if whereCols[i] != c.whereCols[i] {
				t.Errorf("parseSQLStmt(%q) whereCols[%d] = %q, want %q", c.sql, i, whereCols[i], c.whereCols[i])
			}
		}
	}
}

// TestStripSubqueries：P2a——stripSubqueries 只剥 SELECT/WITH/VALUES
// 开头的括号块（等长空白替换）；嵌套子查询整体剥；函数调用/普通逻辑
// 括号原样保留；剥除后剩余 ? 序与外层列序对齐。
func TestStripSubqueries(t *testing.T) {
	blank := func(s string) string { return strings.Repeat(" ", len(s)) }
	cases := []struct{ in, want string }{
		{"x = ? AND EXISTS (SELECT 1 FROM b WHERE y = ?)",
			"x = ? AND EXISTS " + blank("(SELECT 1 FROM b WHERE y = ?)")},
		{"x IN (SELECT y FROM b WHERE z = ?) AND w = ?",
			"x IN " + blank("(SELECT y FROM b WHERE z = ?)") + " AND w = ?"},
		// 嵌套子查询（EXISTS 内再嵌 SELECT）整体剥
		{"x = ? AND EXISTS (SELECT 1 FROM b WHERE y IN (SELECT z FROM c))",
			"x = ? AND EXISTS " + blank("(SELECT 1 FROM b WHERE y IN (SELECT z FROM c))")},
		// 函数调用 / 普通逻辑括号保留
		{"COALESCE(a, b) = ? AND (c = ? OR d = ?)",
			"COALESCE(a, b) = ? AND (c = ? OR d = ?)"},
		// 子查询内占位符同步剥除——剩余 ? 序与外层列序对齐
		{"x = ? AND y = (SELECT MAX(y) FROM b WHERE z = ?)",
			"x = ? AND y = " + blank("(SELECT MAX(y) FROM b WHERE z = ?)")},
	}
	for _, c := range cases {
		if got := stripSubqueries(c.in); got != c.want {
			t.Errorf("stripSubqueries(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestWhereColDollar：Q158——$N 占位符（PostgreSQL 风格）的 WHERE 过滤
// 列提取（go2o memberRepo 用 gof Connector 的 "level= $1" 形态）。
func TestWhereColDollar(t *testing.T) {
	nodes, _, _ := indexFixtureFull(t, map[string]string{
		"go.mod": moduleGoMod,
		"main.go": `package m

import "database/sql"

func f(db *sql.DB) {
	db.QueryRow("SELECT id FROM mm_member WHERE level= $1", 2)
}
`,
	})
	found := false
	for _, n := range nodes {
		if n.Kind == domain.KindFieldAccess && n.Property("type_string") == "sql" &&
			n.Name == "mm_member.level" && n.Property("access_kind") == "filter" {
			found = true
		}
	}
	if !found {
		t.Error("$N 占位符的 WHERE 过滤列未提取（mm_member.level filter 缺失）")
	}
}

// TestSQLQueryCallbackClosure：Q201——Query(sql, callback) 形态的读出值
// 进入回调闭包形参（rows），read 节点边指向闭包形参（归属父函数）
// 而非返回值（返回值与回调形参静态无连接，链断在闭包）。
// 闭包内 rows.Scan(&id) 后 id 参与后续值流，跨函数链贯通
// （go2o 实测：settleRiseData 的 pf_riseinfo.person_id 读出值因闭包
// 断链，person_id → usr_person 键关联缺失）。
func TestSQLQueryCallbackClosure(t *testing.T) {
	nodes, facts := indexFixture(t, map[string]string{
		"go.mod": moduleGoMod,
		"field-summary.yaml": `
summaries:
  - iface: example.com/mtest.Conn
    method: Query
    kind: sql
    sql_write: false
    where_arg: 0
`,
		"main.go": `package m

type Rows struct{}

type Conn interface {
	Query(sql string, cb func(*Rows))
}

func settle(c Conn) {
	var ids []int
	c.Query("SELECT id FROM users WHERE name = ?", func(r *Rows) {
		var id int
		_ = r
		ids = append(ids, id)
	})
	_ = ids
}
`,
	})
	var readID string
	for _, n := range nodes {
		if n.Kind == domain.KindFieldAccess && n.Name == "users.id" &&
			n.Property("access_kind") == "read" {
			readID = string(n.ID)
		}
	}
	if readID == "" {
		t.Fatal("users.id read 节点缺失（SQL 摘要未触发）")
	}
	var outs []string
	for _, f := range facts {
		if f.SourceID == domain.CanonicalID(readID) && f.Kind == domain.FactSummaryIO {
			outs = append(outs, string(f.TargetID))
		}
	}
	found := false
	for _, tgt := range outs {
		if strings.Contains(tgt, "settle#param.r") {
			found = true
		}
	}
	if !found {
		t.Errorf("users.id.read 出边应指向回调闭包形参 settle#param.r（非返回值），got %v", outs)
	}
}



// TestParseSQLSubqueryParen：Q239——子查询右括号不得并进表名/列名
// （(SELECT COUNT(1) FROM mm_member) → mm_member；Q220a 同类 where 串误解析）。
func TestParseSQLSubqueryParen(t *testing.T) {
	cases := []struct {
		sql       string
		table     string
		whereCols []string
	}{
		{"SELECT (SELECT COUNT(1) FROM mm_member) as totalMembers", "mm_member", nil},
		{"SELECT (SELECT COUNT(1) FROM mm_member WHERE status = ?) as n", "mm_member", []string{"status"}},
		{"SELECT * FROM mm_member) WHERE status = ?", "mm_member", []string{"status"}},
		// 子查询 FROM（派生表）按 Q6 精神放弃——不产出假表
		{"SELECT * FROM (SELECT id FROM b) x WHERE x.id = ?", "", nil},
	}
	for _, c := range cases {
		table, _, _, whereCols, _ := parseSQLStmt(c.sql)
		if table != c.table {
			t.Errorf("%q: table = %q, want %q", c.sql, table, c.table)
		}
		if len(whereCols) != len(c.whereCols) {
			t.Errorf("%q: whereCols = %v, want %v", c.sql, whereCols, c.whereCols)
			continue
		}
		for i, w := range c.whereCols {
			if whereCols[i] != w {
				t.Errorf("%q: whereCols[%d] = %q, want %q", c.sql, i, whereCols[i], w)
			}
		}
	}
}








