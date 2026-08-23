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


// astSelect SELECT 语句提取。
func astSelect(s *sqlparser.Select, cte map[string]bool) (string, string, []string, []string, []sqlJoinPair, bool) {
	// CTE 定义名（递归引用不当表）
	if s.With != nil {
		for _, c := range s.With.CTEs {
			cte[c.ID.String()] = true
		}
	}
	tables, cteQual, _ := astCollectTables(s.From, cte)
	if len(tables) == 0 {
		return "", "", nil, nil, nil, false
	}
	table, alias := tables[0].name, tables[0].alias
	// 别名映射（JOIN ON 列前缀解析 + SELECT 列归属）
	aliases := map[string]string{}
	for _, t := range tables {
		aliases[t.name] = t.name
		if t.alias != "" {
			aliases[t.alias] = t.name
		}
	}
	// SELECT 列（AliasedExpr.ColName；* / 函数 / 子查询跳过）。
	// R4：别名列归属——限定符映射到非主表时跳过（n.name 属 nodes，
	// 不得归主表 edges——去限定符全归第一表生成假表.列噪音）；
	// 主表自身别名列（e.source_id）保留
	var cols []string
	if s.SelectExprs != nil {
		for _, e := range s.SelectExprs.Exprs {
			ae, ok := e.(*sqlparser.AliasedExpr)
			if !ok {
				continue
			}
			cn, ok := ae.Expr.(*sqlparser.ColName)
			if !ok {
				continue
			}
			if q := cn.Qualifier.Name.String(); q != "" {
				if aliases[q] != "" && aliases[q] != table {
					continue
				}
			}
			cols = append(cols, cn.Name.String())
		}
	}
	whereCols := astWhereCols(s.Where, aliases, cteQual)
	joinPairs := astJoinPairs(s.From, aliases)
	return table, alias, cols, whereCols, joinPairs, true
}

// astInsert INSERT 语句提取（含 REPLACE INTO——vitess 归为 Insert）。
func astInsert(s *sqlparser.Insert) (string, string, []string, []string, []sqlJoinPair, bool) {
	if s.Table == nil {
		return "", "", nil, nil, nil, false
	}
	if tn, ok := s.Table.Expr.(sqlparser.TableName); ok {
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
	tables, cteQual, _ := astCollectTables(s.TableExprs, cte)
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
	whereCols := astWhereCols(s.Where, aliases, cteQual)
	return table, alias, cols, whereCols, nil, true
}

// astDelete DELETE 语句提取。
func astDelete(s *sqlparser.Delete, cte map[string]bool) (string, string, []string, []string, []sqlJoinPair, bool) {
	tables, cteQual, _ := astCollectTables(s.TableExprs, cte)
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
	whereCols := astWhereCols(s.Where, aliases, cteQual)
	return table, alias, nil, whereCols, nil, true
}



