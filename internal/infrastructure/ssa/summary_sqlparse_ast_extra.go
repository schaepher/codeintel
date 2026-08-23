package ssa

import "vitess.io/vitess/go/vt/sqlparser"

// astCollectTables 遍历 TableExprs 收集真实表（子查询 JOIN/派生表跳过，
// CTE 名跳过）。返回 (表列表, CTE 限定符集合（名+别名，Q252：WHERE
// 里 w.d 的 w 是 walk 的别名——递归分支 CTE 引用列不当真实表列）,
// 是否含子查询)。
func astCollectTables(exprs sqlparser.TableExprs, cte map[string]bool) ([]astTable, map[string]bool, bool) {
	var out []astTable
	cteQual := map[string]bool{}
	derived := false
	var walk func(sqlparser.TableExpr)
	walk = func(e sqlparser.TableExpr) {
		switch t := e.(type) {
		case *sqlparser.AliasedTableExpr:
			if tn, ok := t.Expr.(*sqlparser.TableName); ok {
				name := tn.Name.String()
				if cte[name] {

					cteQual[name] = true
					if t.As.String() != "" {
						cteQual[t.As.String()] = true
					}
					return
				}
				at := astTable{name: name}
				if t.As.String() != "" {
					at.alias = t.As.String()
				}
				out = append(out, at)
				return
			}
			derived = true
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
	return out, cteQual, derived
}

// astWhereCols WHERE 过滤列：比较运算符（= >= <= > < !=）且右操作数是
// 占位符（? / $N）→ 左操作数列名（AST 天然无表前缀；kind='calls' 字面
// 量、json_extract(...) 函数调用不提取——值流只在参数占位符处）。
// Q252 补：CTE 限定符（w.d < ? 的 w 是 walk 别名）跳过——CTE 引用列
// 不当真实表列。
func astWhereCols(w *sqlparser.Where, aliases map[string]string, cteQual map[string]bool) []string {
	if w == nil {
		return nil
	}
	var out []string
	sqlparser.Walk(func(node sqlparser.SQLNode) (bool, error) {
		// P2a：子查询作用域——EXISTS/IN/= (SELECT…) 内部节点属于子查询
		// 作用域，其列名/占位符不得混入外层 WHERE 过滤列
		if _, ok := node.(*sqlparser.Subquery); ok {
			return false, nil
		}
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
			return true, nil
		}
		cn, ok := ce.Left.(*sqlparser.ColName)
		if !ok {
			return true, nil
		}
		if cn.Qualifier.Name.String() != "" && cteQual[cn.Qualifier.Name.String()] {
			return true, nil
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
				// P2a：子查询作用域——ON 内 (SELECT…) 的等值比较属子查询
				// 内部，不得误作 JOIN 键对
				if _, ok := node.(*sqlparser.Subquery); ok {
					return false, nil
				}
				ce, ok := node.(*sqlparser.ComparisonExpr)
				if !ok || ce.Operator != sqlparser.EqualOp {
					return true, nil
				}
				lc, lOK := ce.Left.(*sqlparser.ColName)
				rc, rOK := ce.Right.(*sqlparser.ColName)
				if !lOK || !rOK {
					return true, nil
				}

				from := resolveTable(lc, lTable, rTable, aliases)
				to := resolveTable(rc, lTable, rTable, aliases)
				if from == "" || to == "" {
					return true, nil
				}
				pairs = append(pairs, sqlJoinPair{from, lc.Name.String(), to, rc.Name.String()})
				return true, nil
			}, jt.Condition.On)
		}

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
		return ""
	}
	return ""
}
