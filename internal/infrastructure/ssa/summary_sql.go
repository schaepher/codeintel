package ssa

import (
	"fmt"
	"go/constant"
	"regexp"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
	"golang.org/x/tools/go/ssa"
)

// applySQLSummary 处理 SQL 语句调用（Q97）：SQL 字符串（第 0 实参）解析
// 表名与列名 → 虚拟节点（Name=表.列）；后续值实参按 ? 顺序映射列，
// 发 summary_io 边（字段值 → 虚拟节点）。
// applySQLSummary 处理 SQL 语句摘要：SQL 字符串在 Args[sqlArg]（database/sql
// 的 receiver 后 Args[1]；gof Connector 接口无 receiver 在 Args[0]，Q158），
// 值实参在 sqlArg+1 起（variadic 解包按 ?/$N 顺序映射）。
func (ext *fieldExtractor) applySQLSummary(cc *ssa.CallCommon, calleeID domain.CanonicalID, spec summarySpec, callVal ssa.Value, sqlArg int) error {
	logger := zap.L()
	logger.Debug("enter (fieldExtractor).applySQLSummary")
	defer logger.Debug("exit (fieldExtractor).applySQLSummary")
	if sqlArg < 0 || sqlArg >= len(cc.Args) {
		return nil
	}
	// Q252：多候选还原（phi 分支展开）——每个候选独立解析 emit
	// （同调用点同表列 → 同节点 id，落库 REPLACE 幂等去重）
	sqlCands := ext.resolveSQLCandidates(cc.Args[sqlArg], 0)
	if len(sqlCands) == 0 {
		// Q177 真实形态：Exec(sql interface{}) 常量被 MakeInterface 包装
		if c, ok := unwrapConst(cc.Args[sqlArg]); ok {
			sqlCands = []string{constant.StringVal(c.Value)}
		}
	}
	if len(sqlCands) == 0 {
		return nil
	}
	for _, sqlStr := range sqlCands {
		if err := ext.applySQLSummaryOne(cc, calleeID, spec, callVal, sqlArg, sqlStr); err != nil {
			return err
		}
	}
	return nil
}

// applySQLSummaryOne 单条 SQL 的摘要主体（Q252：多候选各自调用）。
func (ext *fieldExtractor) applySQLSummaryOne(cc *ssa.CallCommon, calleeID domain.CanonicalID, spec summarySpec, callVal ssa.Value, sqlArg int, sqlStr string) error {
	table, tableAlias, cols, whereCols, joinPairs := parseSQLStmt(sqlStr)
	line := ext.prog.Fset.PositionFor(cc.Pos(), false).Line

	// Q250：语句类型覆盖方法名判定——Prepare(INSERT) 场景（内置配置
	// SQLWrite 按 fn=="Exec" 判定，Prepare 恒读）强制走写分支。
	if sqlStmtIsWrite(sqlStr) {
		spec.SQLWrite = true
	}

	if !spec.SQLWrite {
		if table == "" {
			return nil
		}
		// Q250：表节点无条件产出（SELECT 具体列也识别表——relations/ER
		// 表完整性）；列节点只在有列时产出。
		tableID := domain.CanonicalID(string(ext.funcID) + "#ext.sql." + table + ".read@" + fmt.Sprintf("%d", line))
		if err := ext.emit(domain.Item{Node: &domain.CodeEntity{
			ID:        tableID,
			Kind:      domain.KindFieldAccess,
			Name:      table,
			FilePath:  ext.currentFile,
			LineStart: line,
			Properties: map[string]any{
				"full_path":     table,
				"instance_path": table,
				"access_kind":   "read",
				"code_snippet":  sqlStr,
				"type_string":   "sql",
				"is_external":   "true",
				"func_id":       string(ext.funcID),
			},
		}}); err != nil {
			return err
		}
		var callID domain.CanonicalID
		// Q201：Query(sql, callback) 形态——读出值进入回调闭包的形参
		// （rows），read 节点边指向闭包形参（归属父函数）而非调用返回值
		// （返回值与回调形参静态无连接，链断在闭包；go2o 实测
		// settleRiseData 的 pf_riseinfo.person_id 读出值因此断链）。
		// 闭包内 rows.Scan(&i) 后 i 参与后续值流，跨函数链贯通
		if cb := callbackClosureParam(cc); cb != nil {
			callID, _ = ext.emitValue(cb)
		} else if callVal != nil {
			callID, _ = ext.emitValue(callVal)
		}
		if len(cols) == 0 {
			// 纯表级访问（SELECT * / COUNT(*)）——表节点带读出边（旧行为）
			if callID != "" {
				if err := ext.emitEdgeKindLine(tableID, domain.CanonicalID(callID), domain.FactSummaryIO, line); err != nil {
					return err
				}
			}
		}
		for _, col := range cols {
			if col == "" || !validSQLColumn(col) {
				continue // Q250：噪音列（DISTINCT/引号残留）不产出
			}
			name := table + "." + col
			id := domain.CanonicalID(string(ext.funcID) + "#ext.sql." + name + ".read@" + fmt.Sprintf("%d", line))
			if err := ext.emit(domain.Item{Node: &domain.CodeEntity{
				ID:        id,
				Kind:      domain.KindFieldAccess,
				Name:      name,
				FilePath:  ext.currentFile,
				LineStart: line,
				Properties: map[string]any{
					"full_path":     name,
					"instance_path": name,
					"access_kind":   "read",
					"code_snippet":  sqlStr,
					"type_string":   "sql",
					"is_external":   "true",
					"func_id":       string(ext.funcID),
				},
			}}); err != nil {
				return err
			}
			if callID != "" {

				if err := ext.emitEdgeKindLine(id, domain.CanonicalID(callID), domain.FactSummaryIO, line); err != nil {
					return err
				}
			}
		}

		if len(whereCols) > 0 {
			values := []ssa.Value{}
			for i := sqlArg + 1; i < len(cc.Args); i++ {
				values = append(values, variadicElems(cc.Args[i])...)
			}
			for i, arg := range values {
				if i >= len(whereCols) {
					break
				}
				col := sqlColUnqual(table, tableAlias, whereCols[i])
				if !validSQLColumn(col) {
					continue // #247/#249 截断片段或未知前缀
				}
				name := table + "." + col
				id := domain.CanonicalID(string(ext.funcID) + "#ext.sql." + name + ".filter@" + fmt.Sprintf("%d", line))
				if err := ext.emit(domain.Item{Node: &domain.CodeEntity{
					ID:        id,
					Kind:      domain.KindFieldAccess,
					Name:      name,
					FilePath:  ext.currentFile,
					LineStart: line,
					Properties: map[string]any{
						"full_path":     name,
						"instance_path": name,
						"access_kind":   "filter",
						"code_snippet":  sqlStr,
						"type_string":   "sql",
						"is_external":   "true",
						"func_id":       string(ext.funcID),
					},
				}}); err != nil {
					return err
				}
				realArg := arg
				if mi, ok := arg.(*ssa.MakeInterface); ok {
					realArg = mi.X
				}
				argID, err := ext.emitValue(realArg)
				if err != nil || argID == "" {
					continue
				}
				if err := ext.emitEdgeKindLine(argID, id, domain.FactSummaryIO, line); err != nil {
					return err
				}
			}
		}
		// Q239：JOIN ON 键对 → 值流边（from 表列 read → to 表列 filter，
		// origin=join）——JOIN 等值语义 = 值流（A.col 的值 = B.col 筛选），
		// relations BFS 经 data_flows_to 自然吸收为 query 键关联
		for _, jp := range joinPairs {
			if jp.FromTable == "" || jp.FromCol == "" || jp.ToTable == "" || jp.ToCol == "" {
				continue
			}
			fromName := jp.FromTable + "." + jp.FromCol
			toName := jp.ToTable + "." + jp.ToCol
			fromID := domain.CanonicalID(string(ext.funcID) + "#ext.sql." + fromName + ".read@" + fmt.Sprintf("%d", line))
			toID := domain.CanonicalID(string(ext.funcID) + "#ext.sql." + toName + ".filter@" + fmt.Sprintf("%d", line))
			fromProps := map[string]any{
				"full_path":     fromName,
				"instance_path": fromName,
				// Q239：JOIN ON 两侧都是键筛选（a.code = s.city_code 双向）——
				// from 侧标 filter 使 relations BFS 从任一侧出发都归 query
				// （标 read 会因终点 read 归入「间接读」默认不可见）
				"access_kind":  "filter",
				"code_snippet": sqlStr,
				"type_string":  "sql",
				"is_external":  "true",
				"func_id":      string(ext.funcID),
				"origin":       "join",
			}
			toProps := map[string]any{
				"full_path":     toName,
				"instance_path": toName,
				"access_kind":   "filter",
				"code_snippet":  sqlStr,
				"type_string":   "sql",
				"is_external":   "true",
				"func_id":       string(ext.funcID),
				"origin":        "join",
			}
			if err := ext.emit(domain.Item{Node: &domain.CodeEntity{
				ID: fromID, Kind: domain.KindFieldAccess, Name: fromName,
				FilePath: ext.currentFile, LineStart: line, Properties: fromProps,
			}}); err != nil {
				return err
			}
			if err := ext.emit(domain.Item{Node: &domain.CodeEntity{
				ID: toID, Kind: domain.KindFieldAccess, Name: toName,
				FilePath: ext.currentFile, LineStart: line, Properties: toProps,
			}}); err != nil {
				return err
			}
			if err := ext.emitEdgeKindLine(fromID, toID, domain.FactDataFlowsTo, line); err != nil {
				return err
			}
		}
		return nil
	}

	access := "write"
	// Q250：表级 write 节点无条件产出（Prepare 批量写无值实参——
	// 旧逻辑按值循环空转，写目标表完全缺失）。
	if table != "" {
		tableID := domain.CanonicalID(string(ext.funcID) + "#ext.sql." + table + "." + access + "@" + fmt.Sprintf("%d", line))
		if err := ext.emit(domain.Item{Node: &domain.CodeEntity{
			ID:        tableID,
			Kind:      domain.KindFieldAccess,
			Name:      table,
			FilePath:  ext.currentFile,
			LineStart: line,
			Properties: map[string]any{
				"full_path":     table,
				"instance_path": table,
				"access_kind":   access,
				"code_snippet":  sqlStr,
				"type_string":   "sql",
				"is_external":   "true",
				"func_id":       string(ext.funcID),
			},
		}}); err != nil {
			return err
		}
	}
	values := []ssa.Value{}
	for i := sqlArg + 1; i < len(cc.Args); i++ {
		values = append(values, variadicElems(cc.Args[i])...)
	}
	// 无值实参的列节点（Prepare 形态）——写目标列仍可产出（无边；
	// 值流不可得）。有值实参时走下方带边循环。
	if len(values) == 0 {
		for _, col := range cols {
			if col == "" || !validSQLColumn(col) {
				continue
			}
			name := table + "." + col
			id := domain.CanonicalID(string(ext.funcID) + "#ext.sql." + name + "." + access + "@" + fmt.Sprintf("%d", line))
			if err := ext.emit(domain.Item{Node: &domain.CodeEntity{
				ID:        id,
				Kind:      domain.KindFieldAccess,
				Name:      name,
				FilePath:  ext.currentFile,
				LineStart: line,
				Properties: map[string]any{
					"full_path":     name,
					"instance_path": name,
					"access_kind":   access,
					"code_snippet":  sqlStr,
					"type_string":   "sql",
					"is_external":   "true",
					"func_id":       string(ext.funcID),
				},
			}}); err != nil {
				return err
			}
		}
	}
	for i, arg := range values {
		col := ""
		if i < len(cols) {
			col = cols[i]
		}
		// Q252 补：有值实参循环也过滤噪音列（INSERT INTO repos(%s) 的
		// %s 列名——动态列名残留不当列）
		if col != "" && !validSQLColumn(col) {
			continue
		}
		name := table
		if col != "" {
			name = table + "." + col
		}
		if name == "" {
			continue
		}
		realArg := arg
		if mi, ok := arg.(*ssa.MakeInterface); ok {
			realArg = mi.X
		}
		argID, err := ext.emitValue(realArg)
		if err != nil || argID == "" {
			continue
		}
		id := domain.CanonicalID(string(ext.funcID) + "#ext.sql." + name + "." + access + "@" + fmt.Sprintf("%d", line))
		if err := ext.emit(domain.Item{Node: &domain.CodeEntity{
			ID:        id,
			Kind:      domain.KindFieldAccess,
			Name:      name,
			FilePath:  ext.currentFile,
			LineStart: line,
			Properties: map[string]any{
				"full_path":     name,
				"instance_path": name,
				"access_kind":   access,
				"code_snippet":  sqlStr,
				"type_string":   "sql",
				"is_external":   "true",
				"func_id":       string(ext.funcID),
			},
		}}); err != nil {
			return err
		}
		if err := ext.emitEdgeKindLine(argID, id, domain.FactSummaryIO, line); err != nil {
			return err
		}
	}

	if len(whereCols) > 0 {
		for i, col := range whereCols {
			vi := len(cols) + i
			if vi >= len(values) {
				break
			}
			col = sqlColUnqual(table, tableAlias, col)
			if !validSQLColumn(col) {
				continue // #247/#249 截断片段或未知前缀
			}
			name := table + "." + col
			id := domain.CanonicalID(string(ext.funcID) + "#ext.sql." + name + ".filter@" + fmt.Sprintf("%d", line))
			if err := ext.emit(domain.Item{Node: &domain.CodeEntity{
				ID:        id,
				Kind:      domain.KindFieldAccess,
				Name:      name,
				FilePath:  ext.currentFile,
				LineStart: line,
				Properties: map[string]any{
					"full_path":     name,
					"instance_path": name,
					"access_kind":   "filter",
					"code_snippet":  sqlStr,
					"type_string":   "sql",
					"is_external":   "true",
					"func_id":       string(ext.funcID),
				},
			}}); err != nil {
				return err
			}
			realArg := values[vi]
			if mi, ok := realArg.(*ssa.MakeInterface); ok {
				realArg = mi.X
			}
			argID, err := ext.emitValue(realArg)
			if err != nil || argID == "" {
				continue
			}
			if err := ext.emitEdgeKindLine(argID, id, domain.FactSummaryIO, line); err != nil {
				return err
			}
		}
	}
	return nil
}

// callbackClosureParam SQL 调用实参中的回调闭包首参（Query(sql,
// func(rows){...}) 的 rows）：读出值经回调形参进入闭包，闭包内
// Scan(&i) 后 i 参与后续值流。无回调实参返回 nil。
func callbackClosureParam(cc *ssa.CallCommon) ssa.Value {
	for _, a := range cc.Args {
		var fn *ssa.Function
		switch x := a.(type) {
		case *ssa.MakeClosure:
			fn, _ = x.Fn.(*ssa.Function)
		case *ssa.MakeInterface:
			if mc, ok := x.X.(*ssa.MakeClosure); ok {
				fn, _ = mc.Fn.(*ssa.Function)
			}
		}
		if fn != nil && len(fn.Params) > 0 {
			return fn.Params[0]
		}
	}
	return nil
}

// whereColsOf 从 where 条件串提取列名：AND/OR 拆分 + 占位符剥离
// （IN (?) 先处理；其余形态截到最后一个 ? 再 TrimRight 运算符——
// 兼容 " = ?" / "=?" / " <?" / " LIKE ?" 等有无空格写法，以及多行
// 条件串（AND/OR 前后为换行/制表符——pay_order 实测整串未被拆分）。

func whereColsOf(where string) []string {
	var cols []string
	up := strings.ToUpper(where)
	// 尾部子句清理（LIMIT/OFFSET/ORDER BY/GROUP BY/HAVING）——此前残留
	// 进列名（"alias = $1 LIMIT 1" → 整串当列名）
	for _, stop := range []string{" ORDER BY ", " GROUP BY ", " LIMIT ", " OFFSET ", " HAVING "} {
		if j := strings.Index(up, stop); j >= 0 {
			where = where[:j]
			up = strings.ToUpper(where)
			// whereColsOf 从 where 条件串提取列名：AND/OR 拆分 + 占位符剥离
			// （IN (?) 先处理；其余形态截到最后一个 ? 再 TrimRight 运算符——
			// 兼容 " = ?" / "=?" / " <?" / " LIKE ?" 等有无空格写法，以及多行
			// 条件串（AND/OR 前后为换行/制表符——pay_order 实测整串未被拆分）。

		}
	}
	// Q220：AND/OR 拆分大小写不敏感（此前区分大小写——go2o 的 lowercase
	// " and " 整串未拆分 → 列名含 " = ? and ..." 垃圾：pay_merchant.user_type
	// = ? and user_id、ad_data.ad_id=$1 and id 等）
	for _, part := range whereCondRe.Split(where, -1) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if m := whereColLeadRe.FindStringSubmatch(part); m != nil {
			cols = append(cols, m[1])
			continue
		}
		// 无操作符裸列名形态（In("amount")/NotIn("status")）；占位符/
		// 字面量残留（BETWEEN $1 and $2 的 $2）跳过
		if part[0] < 'A' || (part[0] > 'Z' && part[0] < 'a') || part[0] > 'z' {
			continue
		}
		if i := strings.IndexAny(part, " \t\n"); i >= 0 {
			part = part[:i]
		}
		part = strings.Trim(part, "`\"[]()")
		if part != "" {
			cols = append(cols, part)
		}
	}
	return cols
}

// whereCondRe AND/OR 条件拆分（大小写不敏感，\s 覆盖换行/制表符）。
var whereCondRe = regexp.MustCompile(`(?i)\s+(AND|OR)\s+`)

// whereColLeadRe 条件串首列名提取：列名 + 操作符（= / <> / < / > /
// LIKE / BETWEEN / IS / IN），操作符后须接空白或占位符/数字（兼容
// "ad_id=$1" 无空格、"? " 与 "0" 字面量）。列名支持表前缀（b.id）。
var whereColLeadRe = regexp.MustCompile(`(?i)^([A-Za-z_][A-Za-z0-9_.]*)\s*(?:=|<>|<=|>=|<|>|LIKE|BETWEEN|IS|IN)(?:\s|[$\?0-9])`)

// validSQLColumn SQL 列名合法性（#247）：标识符形态（字母开头 + 字母/
// 数字/下划线）+ 非 SQL 关键字 + 非纯数字——SQL 摘要把截断片段
// （nodes.access_kind')、DISTINCT、0/1 等）当列引用的噪音过滤。
// Q252 补：关键字检查小写化（SQL 里 CASE 大写——大小写敏感绕过
// 黑名单，CASE WHEN 表达式被当列名）。
func validSQLColumn(name string) bool {
	if name == "" {
		return false
	}
	c0 := name[0]
	if !(c0 == '_' || ('a' <= c0 && c0 <= 'z') || ('A' <= c0 && c0 <= 'Z')) {
		return false // 数字/符号开头（0、1、'）等）
	}
	for i := 1; i < len(name); i++ {
		c := name[i]
		ok := c == '_' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9')
		if !ok {
			return false
		}
	}
	return !sqlKeyword[strings.ToLower(name)]
}

// sqlKeyword SQL 关键字黑名单（#247：DISTINCT/SELECT 等被误当列名）。
var sqlKeyword = map[string]bool{
	"select": true, "from": true, "where": true, "and": true, "or": true,
	"not": true, "in": true, "on": true, "join": true, "left": true,
	"right": true, "inner": true, "outer": true, "limit": true, "offset": true,
	"order": true, "group": true, "by": true, "having": true, "distinct": true,
	"as": true, "case": true, "when": true, "then": true, "else": true,
	"end": true, "null": true, "true": true, "false": true, "count": true,
	"sum": true, "avg": true, "max": true, "min": true, "exists": true,
	"like": true, "between": true, "is": true, "desc": true, "asc": true,
}

// sqlColUnqual 列别名归一（#249）：`e.source_id` 且 e 是主表别名 →
// source_id（前缀必须是 table 或 alias，否则丢弃返回空）。
func sqlColUnqual(table, alias, col string) string {
	if i := strings.Index(col, "."); i >= 0 {
		pre, rest := col[:i], col[i+1:]
		if pre == table || (alias != "" && pre == alias) {
			return rest
		}
		return "" // 未知前缀（CTE 名/其他表）——丢弃
	}
	return col
}
