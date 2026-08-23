package ssa

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestParseSQLJoinPairs：Q239——JOIN ON 键对提取（sqlJoinPair）。
// INNER/LEFT JOIN + ON 等值键对（别名映射）；CROSS JOIN 无 ON / 逗号
// 连接 / 子查询 JOIN 放弃（无键信号）。
func TestParseSQLJoinPairs(t *testing.T) {
	cases := []struct {
		sql   string
		pairs []sqlJoinPair
	}{

		{"SELECT s.id,a.name FROM sys_sub_station s INNER JOIN sys_district a ON a.code = s.city_code",
			[]sqlJoinPair{{"sys_district", "code", "sys_sub_station", "city_code"}}},
		{"SELECT s.id FROM sys_sub_station s LEFT JOIN sys_district a ON a.code = s.city_code",
			[]sqlJoinPair{{"sys_district", "code", "sys_sub_station", "city_code"}}},
		{"SELECT it.id FROM sale_sub_item it INNER JOIN sale_normal_order ord ON ord.id = it.order_id INNER JOIN mm_member m ON m.member_id = ord.buyer_id",
			[]sqlJoinPair{{"sale_normal_order", "id", "sale_sub_item", "order_id"},
				{"mm_member", "member_id", "sale_normal_order", "buyer_id"}}},

		{"SELECT u.name FROM users u JOIN orders ON u.id = orders.uid",
			[]sqlJoinPair{{"users", "id", "orders", "uid"}}},

		{"SELECT * FROM a JOIN b ON a.x = b.y AND a.m = b.n",
			[]sqlJoinPair{{"a", "x", "b", "y"}, {"a", "m", "b", "n"}}},

		{"SELECT * FROM a CROSS JOIN b", nil},

		{"SELECT * FROM a, b WHERE a.id = b.a_id", nil},

		{"SELECT * FROM a JOIN (SELECT id FROM b) x ON x.id = a.b_id", nil},

		{"SELECT name FROM users WHERE id = ?", nil},
	}
	for _, c := range cases {
		_, _, _, _, pairs := parseSQLStmt(c.sql)
		if len(pairs) != len(c.pairs) {
			t.Errorf("%q: joinPairs = %+v, want %+v", c.sql, pairs, c.pairs)
			continue
		}
		for i, want := range c.pairs {
			if pairs[i] != want {
				t.Errorf("%q: pair[%d] = %+v, want %+v", c.sql, i, pairs[i], want)
			}
		}
	}
}

// TestSQLJoinEmit：Q239——JOIN ON 键对产出 origin=join 的虚拟节点与
// data_flows_to 边（from 表列 read → to 表列 filter）。go2o 真实形态。
func TestSQLJoinEmit(t *testing.T) {
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

func station(c Conn) {
	c.Query("SELECT s.id,a.name FROM sys_sub_station s INNER JOIN sys_district a ON a.code = s.city_code WHERE a.parent IN ($1)", func(r *Rows) {})
}
`,
	})
	// to 侧 filter 节点（origin=join）
	var filterNode *domain.CodeEntity
	for _, n := range nodes {
		if n.Kind != domain.KindFieldAccess {
			continue
		}
		if n.Name == "sys_sub_station.city_code" && n.Property("access_kind") == "filter" {
			filterNode = n
			if n.Property("origin") != "join" {
				t.Errorf("JOIN filter 节点应标 origin=join，got %v", n.Property("origin"))
			}
		}
	}
	if filterNode == nil {
		t.Fatalf("未产出 JOIN to 侧 filter 节点（sys_sub_station.city_code filter）")
	}

	fromID := ""
	for _, n := range nodes {
		if n.Kind == domain.KindFieldAccess && n.Name == "sys_district.code" &&
			n.Property("access_kind") == "filter" {
			fromID = string(n.ID)
			if n.Property("origin") != "join" {
				t.Errorf("JOIN 节点应标 origin=join，got %v", n.Property("origin"))
			}
		}
	}
	if fromID == "" {
		t.Fatalf("未产出 JOIN from 侧 filter 节点（sys_district.code filter）")
	}

	found := false
	for _, f := range facts {
		if string(f.SourceID) == fromID && string(f.TargetID) == string(filterNode.ID) &&
			f.Kind == domain.FactDataFlowsTo {
			found = true
		}
	}
	if !found {
		t.Errorf("缺 JOIN 值流边（sys_district.code → sys_sub_station.city_code）")
	}
}

// TestParseSQLJoinPairsMultiline：Q239——多行 SQL（\\n\\t\\tINNER JOIN）
// ON 段截断残留（INNER 并进表名）修复。
func TestParseSQLJoinPairsMultiline(t *testing.T) {
	sql := `SELECT m.user_code FROM sale_sub_item it
		INNER JOIN sale_normal_order ord ON ord.id = it.order_id
		INNER JOIN mm_member m ON m.member_id = ord.buyer_id
		WHERE it.item_id = $1`
	_, _, _, _, pairs := parseSQLStmt(sql)
	want := []sqlJoinPair{
		{"sale_normal_order", "id", "sale_sub_item", "order_id"},
		{"mm_member", "member_id", "sale_normal_order", "buyer_id"},
	}
	if len(pairs) != len(want) {
		t.Fatalf("多行 JOIN pairs = %+v, want %+v", pairs, want)
	}
	for i, w := range want {
		if pairs[i] != w {
			t.Errorf("pair[%d] = %+v, want %+v", i, pairs[i], w)
		}
	}
}

// TestParseSQLCTE：Q251——递归 CTE（value-trace 形态）的递归引用
// （JOIN back d / FROM walk）不得当表；JOIN ON 段列名过 validSQLColumn
// （动态 SQL 的 %s 占位符残留过滤）。
func TestParseSQLCTE(t *testing.T) {
	cases := []struct {
		sql   string
		table string
		pairs []sqlJoinPair
	}{

		{"WITH RECURSIVE back(id, dir) AS (SELECT id, 0 FROM nodes UNION SELECT e.source_id, 1 FROM edges e JOIN back d ON e.target_id = d.id) SELECT * FROM back",
			"nodes", nil},

		{"WITH RECURSIVE walk(id, dir) AS (SELECT id, 0 FROM nodes UNION ALL SELECT source_id, 1 FROM edges JOIN walk ON edges.target_id = walk.id), reach(id) AS (SELECT id FROM walk) SELECT * FROM nodes WHERE id = ?",
			"nodes", nil},

		{"WITH RECURSIVE walk(src, tgt, d) AS (SELECT source_id, target_id, 1 FROM edges UNION SELECT e.source_id, e.target_id, w.d + 1 FROM edges e JOIN walk w ON e.source_id = w.tgt WHERE w.d < ?) SELECT * FROM edges WHERE source_id = ?",
			"edges", nil},

		{"SELECT * FROM a JOIN b ON a.%s = b.id", "a", nil},

		{"SELECT s.id FROM sys_sub_station s INNER JOIN sys_district a ON a.code = s.city_code",
			"sys_sub_station", []sqlJoinPair{{"sys_district", "code", "sys_sub_station", "city_code"}}},
		// P2a：ON 内子查询作用域——EXISTS (SELECT…) 内部的 a.x = b.y
		// 是相关子查询列引用，不得误作 JOIN 键对（AST 路径；书写顺序
		// 左=From——a.id = b.a_id → {a,id,b,a_id}）
		{"SELECT * FROM a JOIN b ON a.id = b.a_id AND EXISTS (SELECT 1 FROM c WHERE a.x = b.y)",
			"a", []sqlJoinPair{{"a", "id", "b", "a_id"}}},
		// 启发式路径（%s 残留致 vitess 失败降级）：同上
		{"SELECT * FROM a JOIN b ON a.id = b.a_id AND EXISTS (SELECT 1 FROM c WHERE a.x = b.y) WHERE %s = ?",
			"a", []sqlJoinPair{{"a", "id", "b", "a_id"}}},
	}
	for _, c := range cases {
		table, _, _, _, pairs := parseSQLStmt(c.sql)
		if table != c.table {
			t.Errorf("%.40q: table = %q, want %q", c.sql, table, c.table)
			continue
		}
		if len(pairs) != len(c.pairs) {
			t.Errorf("%.40q: joinPairs = %+v, want %+v", c.sql, pairs, c.pairs)
			continue
		}
		for i, want := range c.pairs {
			if pairs[i] != want {
				t.Errorf("%.40q: pair[%d] = %+v, want %+v", c.sql, i, pairs[i], want)
			}
		}
	}
}

// TestSQLWhereCTEAlias：Q252 补——启发式降级路径的 CTE 别名列过滤
// （GetImpact 的 reach 递归分支 `WHERE r.d < ?`——r 是 reach 别名，
// d 不得当 edges 列；主查询无真实表时 AST 降级启发式）。
func TestSQLWhereCTEAlias(t *testing.T) {
	sql := "WITH RECURSIVE reach(id, d) AS (\n    SELECT target_id, 1 FROM edges WHERE source_id = ?\n    UNION\n    SELECT e.target_id, r.d + 1 FROM edges e JOIN reach r ON e.source_id = r.id WHERE r.d < ?\n)"
	_, _, _, whereCols, _ := parseSQLStmt(sql)
	for _, c := range whereCols {
		if c == "d" {
			t.Errorf("CTE 别名列 d 不得当 edges 列: %v", whereCols)
		}
	}
}
