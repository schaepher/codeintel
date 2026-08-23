package ssa

import (
	"regexp"
	"strings"
)

// whereColRe WHERE 过滤列（`列 = ?` 序列）。Q251：前导捕获组排除
// 标识符/点/%（fmt.Sprintf 动态 SQL 的 `%s = ?`——s 是 % 后残留
// 片段，不得当列名；a.s 由 a 起点整串匹配，s 处不重复匹配）。
var whereColRe = regexp.MustCompile(`(^|[^A-Za-z0-9_%.])([A-Za-z_][A-Za-z0-9_.]*)\s*(?:=|>=|<=|>|<|!=|<>)\s*(\?|\$\d+)`)

// sqlJoinPair JOIN ON 等值键对（Q239）：JOIN 表.列 = 主表.列
// （SQL 书写顺序：左 = From，右 = To；别名已映射回真实表名）。
type sqlJoinPair struct {
	FromTable, FromCol string
	ToTable, ToCol     string
}

// sqlKwdSet SQL 关键字集合（别名判定：JOIN 段表名后的单词若是关键字
// 则不是别名——ON/WHERE/ORDER 等）。
var sqlKwdSet = map[string]bool{
	"INNER": true, "LEFT": true, "RIGHT": true, "CROSS": true, "FULL": true,
	"OUTER": true, "JOIN": true, "ON": true, "WHERE": true, "AND": true,
	"OR": true, "ORDER": true, "BY": true, "LIMIT": true, "GROUP": true,
	"HAVING": true, "UNION": true, "SELECT": true, "FROM": true, "SET": true,
	"VALUES": true, "AS": true, "WHEN": true, "THEN": true, "ELSE": true,
	"END": true, "CASE": true, "NOT": true, "IN": true, "IS": true,
	"NULL": true, "BETWEEN": true, "LIKE": true, "EXISTS": true, "DISTINCT": true,
}

// nextToken 取下一 token（空白/括号/逗号分隔）与剩余部分。
func nextToken(s string) (tok, rest string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}
	i := 0
	for i < len(s) {
		c := s[i]
		if c == ' ' || c == '\t' || c == '\n' || c == ',' || c == '(' || c == ')' || c == ';' {
			break
		}
		i++
	}
	return s[:i], strings.TrimSpace(s[i:])
}

// reCTEName CTE 定义名（Q251：WITH [RECURSIVE] name(cols) AS (——
// 递归分支的递归引用（FROM back / JOIN back d）不得当表）。
var reCTEName = regexp.MustCompile(`(?i)([A-Za-z_][A-Za-z0-9_]*)\s*\([^)]*\)\s+AS\s*\(`)

// extractCTENames 提取 SQL 中全部 CTE 定义名（value-trace 递归 CTE
// 形态：back/reach/walk/flows/def_trace/fwd_trace）。
func extractCTENames(sql string) map[string]bool {
	names := map[string]bool{}
	for _, m := range reCTEName.FindAllStringSubmatch(sql, -1) {
		names[m[1]] = true
	}
	return names
}

// splitTopLevelComma 顶层逗号拆分（Q250：括号内不切——COALESCE(a,b)
// 的内部逗号不得把函数段劈成列名残片）。
func splitTopLevelComma(s string) []string {
	var parts []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				parts = append(parts, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	return append(parts, strings.TrimSpace(s[start:]))
}

// splitSQLCond 按 AND/OR 拆分 ON 条件（顶层；括号内不拆——简单扫描）。
func splitSQLCond(cond string) []string {
	var parts []string
	depth := 0
	start := 0
	up := strings.ToUpper(cond)
	for i := 0; i < len(cond); i++ {
		switch cond[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		}
		if depth == 0 && (up[i] == ' ' || up[i] == '\n' || up[i] == '\t') {
			rest := up[i:]
			for _, k := range []string{" AND ", " OR "} {
				if strings.HasPrefix(rest, k) {
					parts = append(parts, strings.TrimSpace(cond[start:i]))
					start = i + len(k)
					i += len(k) - 1
					break
				}
			}
		}
	}
	parts = append(parts, strings.TrimSpace(cond[start:]))
	return parts
}

// splitQualified `a.code` → (a, code)；无点 → ("", token)。
func splitQualified(s string) (string, string) {
	s = strings.Trim(s, "`\"[]")
	if i := strings.LastIndex(s, "."); i >= 0 {
		return s[:i], s[i+1:]
	}
	return "", s
}

// extractJoinPairs 从 FROM 之后剩余段提取 JOIN ON 等值键对（Q239）：
//
//	FROM sys_sub_station s INNER JOIN sys_district a ON a.code = s.city_code
//	  → {sys_district.code → sys_sub_station.city_code}
//
// 覆盖 INNER/LEFT/RIGHT/CROSS JOIN + 别名映射 + AND 多键对；逗号连接 /
// 子查询 JOIN / 无 ON 的 CROSS JOIN 放弃（无键信号）。
func extractJoinPairs(rest string, cte map[string]bool) []sqlJoinPair {
	up := strings.ToUpper(rest)
	if !strings.Contains(up, " JOIN ") {
		return nil
	}
	// 别名映射：FROM 首表 + 首表别名 + 各 JOIN 表
	aliases := map[string]string{}
	firstTok, firstRest := nextToken(rest)
	if firstTok != "" {
		aliases[firstTok] = firstTok
		if a, _ := nextToken(firstRest); a != "" && !sqlKwdSet[strings.ToUpper(a)] && !strings.HasPrefix(a, "(") {
			aliases[a] = firstTok
		}
	}
	// Q251：全局扫描 FROM <表> <别名> 注册（UNION 递归分支的
	// FROM edges e——JOIN 段别名只覆盖 JOIN 表，递归分支表别名缺失
	// 会把 e 当表名）
	for _, m := range reFromAlias.FindAllStringSubmatch(rest, -1) {
		if m[1] == "" {
			continue
		}
		aliases[m[1]] = m[1]
		if m[2] != "" && !sqlKwdSet[strings.ToUpper(m[2])] {
			aliases[m[2]] = m[1]
		}
	}
	var pairs []sqlJoinPair
	for {
		j := strings.Index(up, " JOIN ")
		if j < 0 {
			break
		}
		seg := rest[j+len(" JOIN "):]
		// JOIN 表名（跳过 INNER/LEFT 等已在 JOIN 前）
		tbl, afterTbl := nextToken(seg)
		if tbl == "" {
			break
		}
		// Q251：递归 CTE 引用（JOIN back d）——CTE 名不得当表
		if cte[tbl] {
			rest = afterTbl
			up = strings.ToUpper(rest)
			continue
		}
		aliases[tbl] = tbl
		// 可选别名（非关键字且非 ON 起始）
		if a, afterAlias := nextToken(afterTbl); a != "" && !sqlKwdSet[strings.ToUpper(a)] {
			aliases[a] = tbl
			afterTbl = afterAlias
		}
		// ON 段（兼容 "ON ..." 开头形态——ON 位于段首无前导空格，
		// 偏移 3 而非 4）
		onUp := strings.ToUpper(afterTbl)
		oi := -1
		skip := len(" ON ")
		if strings.HasPrefix(onUp, "ON ") {
			oi = 0
			skip = len("ON ")
		} else {
			oi = strings.Index(onUp, " ON ")
		}
		if oi < 0 {
			break
		}
		onSeg := afterTbl[oi+skip:]
		end := len(onSeg)
		// 修饰符形态在前（更长先匹配——" INNER JOIN " 的 end 比 " JOIN " 早）
		for _, stop := range []string{" INNER JOIN ", " LEFT JOIN ", " RIGHT JOIN ", " CROSS JOIN ",
			" FULL JOIN ", " OUTER JOIN ", " JOIN ", " WHERE ", " ORDER BY ", " LIMIT ",
			" GROUP BY ", " HAVING ", " UNION "} {
			if k := strings.Index(strings.ToUpper(onSeg), stop); k >= 0 && k < end {
				end = k
			}
		}
		onPart := onSeg[:end]
		for _, cond := range splitSQLCond(onPart) {
			eq := strings.Index(cond, "=")
			if eq < 0 {
				continue
			}
			l := strings.TrimSpace(cond[:eq])
			r := strings.TrimSpace(cond[eq+1:])
			// 多行 SQL 的 ON 段截断后右操作数可能带尾随 INNER/LEFT 等
			// 残留（\n\t\tINNER JOIN 的 stop 前导空格匹配不到）——
			// 取首 token 防残留并进表名
			if i := strings.IndexAny(l, " \t\n"); i >= 0 {
				l = l[:i]
			}
			if i := strings.IndexAny(r, " \t\n"); i >= 0 {
				r = r[:i]
			}
			lt, lc := splitQualified(l)
			rt, rc := splitQualified(r)
			// Q251：右括号残留剥离（`d.id)` 截断）+ 列名合法性
			// （动态 SQL 的 %s 占位符残留过滤）
			lc = strings.TrimRight(lc, ")")
			rc = strings.TrimRight(rc, ")")
			if lc == "" || rc == "" || !validSQLColumn(lc) || !validSQLColumn(rc) {
				continue
			}
			lT := aliases[lt]
			if lT == "" {
				lT = lt
			}
			rT := aliases[rt]
			if rT == "" {
				rT = rt
			}
			pairs = append(pairs, sqlJoinPair{lT, lc, rT, rc})
		}
		// 前进防死循环
		if j+len(" JOIN ") >= len(rest) {
			break
		}
		rest = afterTbl
		up = strings.ToUpper(rest)
	}
	return pairs
}

// extractWhereCols 从 SQL 语句剩余部分提取 WHERE 子句的过滤列
// （`列 = ?` 序列，值实参按 ? 顺序映射——表关联分析的数据基础）。
// 支持 a.y = ? 表前缀（去前缀）；WHERE 缺失返回 nil。
func extractWhereCols(rest string) []string {
	up := strings.ToUpper(rest)
	wi := strings.Index(up, " WHERE ")
	if wi < 0 {
		return nil
	}
	wherePart := rest[wi+len(" WHERE "):]
	upPart := strings.ToUpper(wherePart)
	for _, stop := range []string{" ORDER BY ", " LIMIT ", " GROUP BY ", " HAVING ", " UNION "} {
		if j := strings.Index(upPart, stop); j >= 0 {
			wherePart = wherePart[:j]
			break
		}
	}
	var out []string
	for _, m := range whereColRe.FindAllStringSubmatch(wherePart, -1) {
		c := m[2] // m[1] 是前导字符（Q251：排除 %s 残留）
		if i := strings.LastIndex(c, "."); i >= 0 {
			c = c[i+1:] // #249：别名前缀剥离（多表 SQL 兼容）
		}
		c = strings.Trim(c, "`\"[]")
		c = strings.TrimRight(c, ")") // Q239：子查询闭合括号剥离
		if c != "" {
			out = append(out, c)
		}
	}
	return out
}

// parseSQLStmt 从 SQL 语句提取表名、列名与 WHERE 过滤列（Q97 启发式，
// 不做完整 SQL 解析）：
//
//	INSERT INTO t(a, b) VALUES(?, ?)  → t, [a b], nil
//	UPDATE t SET a=?, b=?             → t, [a b], nil
//	DELETE FROM t                     → t, [], nil
//	SELECT a, b FROM t                → t, [a b], nil（P0-2 读路径）
//	SELECT * FROM t                   → t, [], nil（表级）
//	... WHERE y = ?                   → ..., [], [y]（表关联：值实参按 ? 顺序映射）

// sqlStmtRe 语句类型词边界匹配（Q250：子串 Contains 会把列名
// updated_at 误判为 UPDATE——词边界后 SELECT/DDL 里的 updated_at
// 不再切出假表 d_at）。
var (
	reInsertInto = regexp.MustCompile(`(?i)\bINSERT\s+INTO\b`)
	reUpdate     = regexp.MustCompile(`(?i)\bUPDATE\b`)
	reDeleteFrom = regexp.MustCompile(`(?i)\bDELETE\s+FROM\b`)
	// reFrom 词边界 FROM（Q250：多行 SQL 的 \n\t\tFROM 前是 tab/换行，
	// " FROM " 子串匹配不到——SELECT 列表多列时表识别缺失）。
	reFrom = regexp.MustCompile(`(?i)\bFROM\b`)
	// reInsertOrReplace：SQLite INSERT OR REPLACE/IGNORE 与 REPLACE INTO
	// 形态（repo_write 批量写；REPLACE 词边界防 replace_xxx 列误命中）。
	reInsertOrReplace = regexp.MustCompile(`(?i)\b(?:REPLACE|INSERT\s+(?:OR\s+(?:REPLACE|IGNORE|ABORT|ROLLBACK|FAIL)\s+)?)\s+INTO\b`)
	// reFromAlias FROM <表> <别名>（Q251：UNION 递归分支别名注册）。
	reFromAlias = regexp.MustCompile(`(?i)\bFROM\s+([A-Za-z_][A-Za-z0-9_]*)(?:\s+([A-Za-z_][A-Za-z0-9_]*))?`)
	// reWriteStmt 语句级写判定（Q250：Prepare(INSERT) 场景——内置配置
	// 按方法名判 SQLWrite（Exec→写），Prepare 恒读；改按语句类型强制
	// 修正。词边界匹配防 updated_at/replace_xxx 列名误命中）。
	reWriteStmt = regexp.MustCompile(`(?i)\b(?:INSERT\s+(?:OR\s+(?:REPLACE|IGNORE|ABORT|ROLLBACK|FAIL)\s+)?INTO|REPLACE\s+INTO|UPDATE|DELETE\s+FROM)`)
)

// sqlStmtIsWrite SQL 语句是否为写（INSERT/REPLACE/UPDATE/DELETE）。
func sqlStmtIsWrite(sql string) bool {
	return reWriteStmt.MatchString(sql)
}

func parseSQLStmt(sql string) (table, alias string, cols []string, whereCols []string, joinPairs []sqlJoinPair) {
	upper := strings.ToUpper(sql)
	// Q251：CTE 定义名（递归引用不得当表）
	cte := extractCTENames(sql)
	rest := ""
	switch {
	case reInsertOrReplace.MatchString(upper):
		rest = sql[reInsertOrReplace.FindStringIndex(upper)[1]:]
	case reInsertInto.MatchString(upper):
		rest = sql[reInsertInto.FindStringIndex(upper)[1]:]
	case reUpdate.MatchString(upper):
		rest = sql[reUpdate.FindStringIndex(upper)[1]:]
	case reDeleteFrom.MatchString(upper):
		rest = sql[reDeleteFrom.FindStringIndex(upper)[1]:]
	case reFrom.MatchString(upper):

		fromIdx := reFrom.FindStringIndex(upper)[0]
		rest = sql[fromIdx+len("FROM"):]
		joinPairs = extractJoinPairs(rest, cte)
		if strings.Contains(upper, "SELECT ") {
			selPart := strings.TrimSpace(sql[strings.Index(upper, "SELECT ")+len("SELECT ") : fromIdx])
			// Q250：DISTINCT/ALL 前缀剥离（DISTINCT 关键字不得并进列名）
			selUp := strings.ToUpper(selPart)
			for _, kw := range []string{"DISTINCT ", "ALL "} {
				if strings.HasPrefix(selUp, kw) {
					selPart = strings.TrimSpace(selPart[len(kw):])
					selUp = strings.ToUpper(selPart)
					break
				}
			}
			if selPart != "" && !strings.Contains(selUp, "*") {
				for _, c := range splitTopLevelComma(selPart) {
					if i := strings.Index(c, " "); i >= 0 {
						c = c[:i]
					}
					if i := strings.LastIndex(c, "."); i >= 0 {
						c = c[i+1:]
					}
					c = strings.Trim(c, "`\"[]'")
					if c != "" && !strings.Contains(c, "(") && validSQLColumn(c) {
						cols = append(cols, c)
					}
				}
			}
		}
	default:
		return "", "", nil, nil, nil
	}
	rest = strings.TrimSpace(rest)

	tableEnd := len(rest)
	for i := 0; i < len(rest); i++ {
		if rest[i] == '(' || rest[i] == ' ' || rest[i] == '\t' || rest[i] == '\n' || rest[i] == ';' {
			tableEnd = i
			break
		}
	}
	table = strings.TrimSpace(rest[:tableEnd])
	table = strings.Trim(table, "`\"[]")
	// Q239：子查询右括号不得并进表名（(SELECT ... FROM mm_member) → mm_member）
	table = strings.TrimRight(table, ")")
	// Q251：主表是 CTE 定义名（WITH RECURSIVE back(...) 的外部查询
	// FROM back）——不得当表
	if cte[table] {
		return "", "", nil, nil, nil
	}
	if table == "" {
		return "", "", nil, nil, nil
	}
	// 主表别名（#249：FROM edges e → alias=e——WHERE 列别名归一用）
	after0 := strings.TrimSpace(rest[tableEnd:])
	if after0 != "" && after0[0] != '(' {
		if i := strings.IndexAny(after0, " \t\n,;("); i > 0 {
			alias = strings.TrimSpace(after0[:i])
			alias = strings.Trim(alias, "`\"[]")
		}
	}
	if alias != "" {
		up := strings.ToUpper(alias)
		if sqlKwdSet[up] || up == "WHERE" || up == "JOIN" || up == "SET" {
			alias = "" // 关键字不是别名
		}
	}

	after := strings.TrimSpace(rest[tableEnd:])
	if strings.HasPrefix(after, "(") {

		inner := after[1:]
		if i := strings.Index(inner, ")"); i >= 0 {
			inner = inner[:i]
		}
		for _, c := range strings.Split(inner, ",") {
			c = strings.TrimSpace(c)
			c = strings.Trim(c, "`\"[]")
			if c != "" {
				cols = append(cols, c)
			}
		}
	} else if strings.Contains(upper, " SET ") {

		up := strings.ToUpper(rest)
		if i := strings.Index(up, " SET "); i >= 0 {
			setPart := rest[i+len(" SET "):]
			if j := strings.Index(setPart, " WHERE"); j >= 0 {
				setPart = setPart[:j]
			}
			for _, c := range strings.Split(setPart, ",") {
				c = strings.TrimSpace(c)
				if k := strings.Index(c, "="); k >= 0 {
					c = strings.TrimSpace(c[:k])
					c = strings.Trim(c, "`\"[]")
					if c != "" {
						cols = append(cols, c)
					}
				}
			}
		}
	}

	whereCols = extractWhereCols(rest)
	return table, alias, cols, whereCols, joinPairs
}
