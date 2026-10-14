package ssa

// SQL 解析结果构建级缓存（Q?）：同一原始 SQL 在多个调用点/多个 extractor
// 重复执行完整解析器（失败还可能重试转义版 + 启发式提取）。按原始 SQL
// 缓存表/别名/列/过滤列/关联对（含失败结果），有规模上限、只读共享。
//
// 统计口径：降级统计保持**按调用**计数语义——缓存命中时按首次解析的
// outcome 补计 sqlAstOK/sqlAstFail；classifyHeuristic 已内部去重，命中
// 不重复分类。VACUUM/PRAGMA 等短路命令不占降级计数（outcome=NoCount）。

// parseSQLStmtCached SQL 解析（构建级缓存）：语义与 parseSQLStmt 一致，
// 统计口径不变。
func (ext *fieldExtractor) parseSQLStmtCached(sql string) (table, alias string, cols, whereCols []string, joinPairs []sqlJoinPair) {
	if ext != nil && ext.funcs != nil {
		if e, ok := ext.funcs.sqlParseCached(sql); ok {
			switch e.outcome {
			case sqlOutcomeAST:
				sqlAstOK.Add(1)
			case sqlOutcomeHeuristic:
				sqlAstFail.Add(1)
			}
			return e.table, e.alias, e.cols, e.whereCols, e.joinPairs
		}
	}
	beforeOK, beforeFail := sqlAstOK.Load(), sqlAstFail.Load()
	table, alias, cols, whereCols, joinPairs = parseSQLStmt(sql)
	outcome := sqlOutcomeNoCount
	switch {
	case sqlAstOK.Load() > beforeOK:
		outcome = sqlOutcomeAST
	case sqlAstFail.Load() > beforeFail:
		outcome = sqlOutcomeHeuristic
	}
	if ext != nil && ext.funcs != nil {
		ext.funcs.storeSQLParse(sql, sqlParseEntry{
			table: table, alias: alias, cols: cols, whereCols: whereCols,
			joinPairs: joinPairs, outcome: outcome,
		})
	}
	return table, alias, cols, whereCols, joinPairs
}
