package ssa

// Q252 混合方案主路径：vitess 专业 SQL 解析器（AST 精确提取表/列/
// WHERE 过滤列/JOIN 键对）——Q220a/Q247/Q249/Q250/Q251 的启发式形态
// bug 由解析器根治（词边界/列段拆分/CTE 作用域/语句类型判定是解析器
// 的本职）。parse error（动态 SQL %s 残留、SQLite 特有语法 INSERT OR
// REPLACE/GLOB）时降级现有启发式 parseSQLStmtHeuristic。

import (
	"vitess.io/vitess/go/vt/sqlparser"
)

// astParser 单例解析器（vitess v0.24：New 创建实例，线程安全可复用）。
var astParser = mustASTParser()

func mustASTParser() *sqlparser.Parser {
	p, err := sqlparser.New(sqlparser.Options{})
	if err != nil {
		panic(err)
	}
	return p
}

// parseSQLStmtAST 主路径：AST → (table, alias, cols, whereCols, joinPairs)。
// ok=false（解析失败/语句类型不支持/主表是 CTE 或无表）时调用方降级。
func parseSQLStmtAST(sql string) (table, alias string, cols []string, whereCols []string, joinPairs []sqlJoinPair, ok bool) {
	stmt, err := astParser.Parse(sql)
	if err != nil {
		return "", "", nil, nil, nil, false
	}
	cte := map[string]bool{}
	switch s := stmt.(type) {
	case *sqlparser.Select:
		return astSelect(s, cte)
	case *sqlparser.Insert:
		return astInsert(s)
	case *sqlparser.Update:
		return astUpdate(s, cte)
	case *sqlparser.Delete:
		return astDelete(s, cte)
	}
	return "", "", nil, nil, nil, false
}

// astTable AST 表引用（名 + 别名）。
type astTable struct {
	name  string
	alias string
}

// astCollectTables 遍历 TableExprs 收集真实表（子查询 JOIN/派生表跳过，
// CTE 名跳过）。返回 (表列表, 是否含子查询)。
func astCollectTables(exprs sqlparser.TableExprs, cte map[string]bool) ([]astTable, bool) {
	var out []astTable
	derived := false
	var walk func(sqlparser.TableExpr)
	walk = func(e sqlparser.TableExpr) {
		switch t := e.(type) {
		case *sqlparser.AliasedTableExpr:
			if tn, ok := t.Expr.(*sqlparser.TableName); ok {
				name := tn.Name.String()
				if cte[name] {
					return // Q251：递归 CTE 引用不当表
				}
				at := astTable{name: name}
				if t.As.String() != "" {
					at.alias = t.As.String()
				}
				out = append(out, at)
				return
			}
			derived = true // DerivedTable 等
		case *sqlparser.JoinTableExpr:
			walk(t.LeftExpr)
			walk(t.RightExpr)
		case *sqlparser.ParenTableExpr:
			for _, sub := range t.Exprs {
				walk(sub)
			}
		default:
			derived = true
		}
	}
	for _, e := range exprs {
		walk(e)
	}
	return out, derived
}

// astSelect SELECT 语句提取。
func astSelect(s *sqlparser.Select, cte map[string]bool) (string, string, []string, []string, []sqlJoinPair, bool) {
	// CTE 定义名（递归引用不当表）
	if s.With != nil {
		for _, c := range s.With.CTEs {
			cte[c.ID.String()] = true
		}
	}
	tables, _ := astCollectTables(s.From, cte)
	if len(tables) == 0 {
		return "", "", nil, nil, nil, false
	}
	table, alias := tables[0].name, tables[0].alias
	// SELECT 列（AliasedExpr.ColName；* / 函数 / 子查询跳过）
	var cols []string
	if s.SelectExprs != nil {
		for _, e := range s.SelectExprs.Exprs {
			ae, ok := e.(*sqlparser.AliasedExpr)
			if !ok {
				continue
			}
			if cn, ok := ae.Expr.(*sqlparser.ColName); ok {
				cols = append(cols, cn.Name.String())
			}
		}
	}
	// 别名映射（JOIN ON 列前缀解析）
	aliases := map[string]string{}
	for _, t := range tables {
		aliases[t.name] = t.name
		if t.alias != "" {
			aliases[t.alias] = t.name
		}
	}
	whereCols := astWhereCols(s.Where, aliases)
	joinPairs := astJoinPairs(s.From, aliases)
	return table, alias, cols, whereCols, joinPairs, true
}

// astInsert INSERT 语句提取（含 REPLACE INTO——vitess 归为 Insert）。
func astInsert(s *sqlparser.Insert) (string, string, []string, []string, []sqlJoinPair, bool) {
	if s.Table == nil {
		return "", "", nil, nil, nil, false
	}
	if tn, ok := s.Table.Expr.(*sqlparser.TableName); ok {
		table := tn.Name.String()
		if table == "" {
			return "", "", nil, nil, nil, false
		}
		var cols []string
		for _, c := range s.Columns {
			cols = append(cols, c.String())
		}
		return table, "", cols, nil, nil, true
	}
	return "", "", nil, nil, nil, false
}

// astUpdate UPDATE 语句提取（SET 列 + WHERE 过滤列）。
func astUpdate(s *sqlparser.Update, cte map[string]bool) (string, string, []string, []string, []sqlJoinPair, bool) {
	tables, _ := astCollectTables(s.TableExprs, cte)
	if len(tables) == 0 {
		return "", "", nil, nil, nil, false
	}
	table, alias := tables[0].name, tables[0].alias
	var cols []string
	for _, e := range s.Exprs {
		if e.Name != nil {
			cols = append(cols, e.Name.Name.String())
		}
	}
	aliases := map[string]string{}
	for _, t := range tables {
		aliases[t.name] = t.name
		if t.alias != "" {
			aliases[t.alias] = t.name
		}
	}
	whereCols := astWhereCols(s.Where, aliases)
	return table, alias, cols, whereCols, nil, true
}

// astDelete DELETE 语句提取。
func astDelete(s *sqlparser.Delete, cte map[string]bool) (string, string, []string, []string, []sqlJoinPair, bool) {
	tables, _ := astCollectTables(s.TableExprs, cte)
	if len(tables) == 0 {
		return "", "", nil, nil, nil, false
	}
	table, alias := tables[0].name, tables[0].alias
	aliases := map[string]string{}
	for _, t := range tables {
		aliases[t.name] = t.name
		if t.alias != "" {
			aliases[t.alias] = t.name
		}
	}
	whereCols := astWhereCols(s.Where, aliases)
	return table, alias, nil, whereCols, nil, true
}

// astWhereCols WHERE 过滤列：比较运算符（= >= <= > < !=）且右操作数是
// 占位符（? / $N）→ 左操作数列名（AST 天然无表前缀；kind='calls' 字面
// 量、json_extract(...) 函数调用不提取——值流只在参数占位符处）。
func astWhereCols(w *sqlparser.Where, aliases map[string]string) []string {
	if w == nil {
		return nil
	}
	var out []string
	sqlparser.Walk(func(node sqlparser.SQLNode) (bool, error) {
		ce, ok := node.(*sqlparser.ComparisonExpr)
		if !ok {
			return true, nil
		}
		switch ce.Operator {
		case sqlparser.EqualOp, sqlparser.NotEqualOp, sqlparser.LessThanOp,
			sqlparser.LessEqualOp, sqlparser.GreaterThanOp, sqlparser.GreaterEqualOp:
		default:
			return true, nil
		}
		if _, ok := ce.Right.(*sqlparser.Argument); !ok {
			return true, nil // 右侧非占位符（字面量/列）——无值流
		}
		cn, ok := ce.Left.(*sqlparser.ColName)
		if !ok {
			return true, nil // 左侧函数/表达式
		}
		out = append(out, cn.Name.String())
		return true, nil
	}, w.Expr)
	return out
}

// astJoinPairs JOIN ON 等值键对（与启发式 extractJoinPairs 同语义）：
// A JOIN B ON a.x = b.y → {A, x, B, y}（书写顺序左=From 右=To；别名
// 映射回真实表名）。AND 多键对 / INNER/LEFT 均覆盖；子查询 JOIN 跳过。
func astJoinPairs(from sqlparser.TableExprs, aliases map[string]string) []sqlJoinPair {
	var pairs []sqlJoinPair
	var walk func(sqlparser.TableExpr)
	walk = func(e sqlparser.TableExpr) {
		jt, ok := e.(*sqlparser.JoinTableExpr)
		if !ok {
			return
		}
		// 两侧必须是真实表（子查询 JOIN 放弃）
		lt, lOK := jt.LeftExpr.(*sqlparser.AliasedTableExpr)
		rt, rOK := jt.RightExpr.(*sqlparser.AliasedTableExpr)
		if !lOK || !rOK {
			walk(jt.LeftExpr)
			walk(jt.RightExpr)
			return
		}
		lTn, lTbl := lt.Expr.(*sqlparser.TableName)
		rTn, rTbl := rt.Expr.(*sqlparser.TableName)
		if !lTbl || !rTbl {
			return
		}
		lTable, rTable := lTn.Name.String(), rTn.Name.String()
		if lTable == "" || rTable == "" {
			return
		}
		if jt.Condition != nil && jt.Condition.On != nil {
			sqlparser.Walk(func(node sqlparser.SQLNode) (bool, error) {
				ce, ok := node.(*sqlparser.ComparisonExpr)
				if !ok || ce.Operator != sqlparser.EqualOp {
					return true, nil
				}
				lc, lOK := ce.Left.(*sqlparser.ColName)
				rc, rOK := ce.Right.(*sqlparser.ColName)
				if !lOK || !rOK {
					return true, nil
				}
				// 书写顺序：左 = From，右 = To（与启发式一致）
				from := resolveTable(lc, lTable, rTable, aliases)
				to := resolveTable(rc, lTable, rTable, aliases)
				if from == "" || to == "" {
					return true, nil
				}
				pairs = append(pairs, sqlJoinPair{from, lc.Name.String(), to, rc.Name.String()})
				return true, nil
			}, jt.Condition.On)
		}
		// 嵌套 JOIN（左侧链）继续
		walk(jt.LeftExpr)
		walk(jt.RightExpr)
	}
	for _, e := range from {
		walk(e)
	}
	return pairs
}

// resolveTable JOIN ON 列前缀 → 真实表名（无前缀/未知前缀 → 取 JOIN
// 对侧默认——启发式行为：无别名 JOIN 用表名本身）。
func resolveTable(cn *sqlparser.ColName, lTable, rTable string, aliases map[string]string) string {
	if cn.Qualifier.Name.String() != "" {
		if t := aliases[cn.Qualifier.Name.String()]; t != "" {
			return t
		}
		return "" // 未知前缀（CTE 等）——丢弃
	}
	return "" // 无前缀的 ON 列（少见）——启发式也丢弃（需要两侧限定）
}
