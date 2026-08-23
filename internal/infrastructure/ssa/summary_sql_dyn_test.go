package ssa

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestSQLDynamicSprintf：Q239——fmt.Sprintf 动态拼接 SQL 还原：
// `...WHERE %s` 的 %s ← where 实参（常量 / 嵌套 Sprintf / 跨函数参数）
// → 还原后走统一解析（表/where 列）。go2o rbac_dao_impl 真实形态。
func TestSQLDynamicSprintf(t *testing.T) {
	nodes, _ := indexFixture(t, map[string]string{
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

import "fmt"

type Rows struct{}

type Conn interface {
	Query(sql string, cb func(*Rows))
}

func pagingPermRole(c Conn, begin, end int, where string) {
	s := fmt.Sprintf("SELECT COUNT(1) FROM rbac_role WHERE %s", where)
	c.Query(s, func(r *Rows) {})
}

func caller(c Conn) {
	pagingPermRole(c, 0, 10, "role_id = ?")
}

func main() {}
`,
	})
	var tableNode, filterNode *domain.CodeEntity
	for _, n := range nodes {
		if n.Kind != domain.KindFieldAccess {
			continue
		}
		if n.Name == "rbac_role" || n.Name == "rbac_role.role_id" {
			if n.Name == "rbac_role" && n.Property("access_kind") == "read" {
				tableNode = n
			}
			if n.Name == "rbac_role.role_id" && n.Property("access_kind") == "filter" {
				filterNode = n
			}
		}
	}
	if tableNode == nil {
		t.Errorf("动态 SQL 未还原表 rbac_role（Sprintf %%s 未解析）")
	}
	if filterNode == nil {
		t.Errorf("动态 SQL 未还原 where 列 role_id（跨函数参数追溯失败）")
	}
}

// TestSQLSelectColEmitsTable：Q250——SELECT 具体列（非 *）也产出表节点
// （relations/ER 表完整性），且列段噪音（DISTINCT 前缀/函数内逗号/
// 引号残留）不产出列节点。
func TestSQLSelectColEmitsTable(t *testing.T) {
	nodes, _ := indexFixture(t, map[string]string{
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
	Query(sql string, cb func(*Rows), args ...any)
}

func latest(c Conn) {
	c.Query("SELECT COALESCE(commit_sha,''), timestamp FROM build_metadata ORDER BY timestamp DESC LIMIT 1", func(r *Rows) {})
}

func origins(c Conn) {
	c.Query("SELECT DISTINCT callee_id FROM summary_origins WHERE function_id = ?", func(r *Rows) {}, "f")
}
`,
	})
	got := map[string]string{}
	for _, n := range nodes {
		if n.Kind == domain.KindFieldAccess && n.Property("type_string") == "sql" {
			got[n.Name] = n.Property("access_kind")
		}
	}
	if got["build_metadata"] != "read" {
		t.Errorf("SELECT 列查询未产出表节点 build_metadata（got %v）", got["build_metadata"])
	}
	if got["summary_origins"] != "read" {
		t.Errorf("SELECT DISTINCT 查询未产出表节点 summary_origins（got %v）", got["summary_origins"])
	}
	if got["build_metadata.timestamp"] != "read" {
		t.Errorf("缺列节点 build_metadata.timestamp（got %v）", got["build_metadata.timestamp"])
	}
	if got["summary_origins.callee_id"] != "read" {
		t.Errorf("缺列节点 summary_origins.callee_id（got %v）", got["summary_origins.callee_id"])
	}
	if got["summary_origins.function_id"] != "filter" {
		t.Errorf("WHERE 过滤列节点缺失：summary_origins.function_id（got %v）", got["summary_origins.function_id"])
	}
	for _, noise := range []string{"summary_origins.DISTINCT", "build_metadata.''", "build_metadata.'')"} {
		if got[noise] != "" {
			t.Errorf("噪音列节点不应产出：%s（got %v）", noise, got[noise])
		}
	}
}

// TestSQLPrepareWriteByStmtType：Q250——Prepare 批量写形态。内置配置
// 按方法名判 SQLWrite（Exec→写），Prepare 恒读；语句类型 INSERT
// （INSERT OR REPLACE）强制修正为写（write 节点）。
func TestSQLPrepareWriteByStmtType(t *testing.T) {
	nodes, _ := indexFixture(t, map[string]string{
		"go.mod": moduleGoMod,
		"field-summary.yaml": `
summaries:
  - iface: example.com/mtest.Conn
    method: Prepare
    kind: sql
    sql_write: false
`,
		"main.go": `package m

type Stmt struct{}

type Conn interface {
	Prepare(sql string) *Stmt
}

func save(c Conn) {
	c.Prepare("INSERT OR REPLACE INTO users(name, email) VALUES(?, ?)")
}
`,
	})
	got := map[string]string{}
	for _, n := range nodes {
		if n.Kind == domain.KindFieldAccess && n.Property("type_string") == "sql" {
			got[n.Name] = n.Property("access_kind")
		}
	}
	if got["users"] != "write" {
		t.Errorf("Prepare(INSERT) 应产出 write 表节点（got %v）", got["users"])
	}
	if got["users.name"] != "write" || got["users.email"] != "write" {
		t.Errorf("Prepare(INSERT) 列节点应为 write（got %v/%v）", got["users.name"], got["users.email"])
	}
}

// TestSQLAliasNormalize：#249 别名归一——FROM edges e 的 WHERE 列
// e.source_id 归一为 source_id；未知前缀丢弃。
func TestSQLAliasNormalize(t *testing.T) {
	cases := []struct {
		sql       string
		table     string
		whereCols []string
	}{
		{"SELECT source_id FROM edges e WHERE e.kind = ? AND e.confidence >= ?", "edges", []string{"kind", "confidence"}},
		{"UPDATE nodes SET name = ? WHERE nodes.id = ?", "nodes", []string{"id"}},
		{"SELECT x FROM a x JOIN b ON a.id = b.a_id WHERE x.id = ?", "a", []string{"id"}},
	}
	for _, c := range cases {
		table, _, _, whereCols, _ := parseSQLStmt(c.sql)
		if table != c.table {
			t.Errorf("parseSQLStmt(%q) table = %q, want %q", c.sql, table, c.table)
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

// TestSQLDynamicSprintfPhi：Q252 扩展——if/else 分支赋值的 Sprintf
// 实参（SSA phi）按分支展开多候选：每个分支的常量各还原一次，全部
// 候选的提取并集（walkEdges 的 anchor=source_id/target_id 形态）。
func TestSQLDynamicSprintfPhi(t *testing.T) {
	nodes, _ := indexFixture(t, map[string]string{
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

import "fmt"

type Rows struct{}

type Conn interface {
	Query(sql string, cb func(*Rows))
}

func walk(c Conn, dir string) {
	anchor := "source_id"
	if dir == "backward" {
		anchor = "target_id"
	}
	q := fmt.Sprintf("SELECT * FROM edges WHERE %s = ? AND kind = 'calls'", anchor)
	c.Query(q, func(r *Rows) {})
}
`,
	})
	got := map[string]string{}
	for _, n := range nodes {
		if n.Kind == domain.KindFieldAccess && n.Property("type_string") == "sql" {
			got[n.Name] = n.Property("access_kind")
		}
	}
	if got["edges.source_id"] != "filter" {
		t.Errorf("phi 分支 source_id 未还原（got %v）", got["edges.source_id"])
	}
	if got["edges.target_id"] != "filter" {
		t.Errorf("phi 分支 target_id 未还原（got %v）", got["edges.target_id"])
	}
	for _, noise := range []string{"edges.s", "edges.%s"} {
		if got[noise] != "" {
			t.Errorf("占位符残留不应产出：%s", noise)
		}
	}
}
