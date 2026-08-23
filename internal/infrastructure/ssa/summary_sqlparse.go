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


// extractWhereCols 从 SQL 语句剩余部分提取 WHERE 子句的过滤列
// （`列 = ?` 序列，值实参按 ? 顺序映射——表关联分析的数据基础）。
// 支持 a.y = ? 表前缀（去前缀）；WHERE 缺失返回 nil。
func extractWhereCols(rest string, cte map[string]bool) []string {
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
	// P2a：子查询作用域——EXISTS/IN/= (SELECT…) 内部列名/占位符属于
	// 子查询，正则全文匹配会泄漏；先整体剥离子查询块（保空白对齐）
	wherePart = stripSubqueries(wherePart)
	var out []string
	for _, m := range whereColRe.FindAllStringSubmatch(wherePart, -1) {
		c := m[2] // m[1] 是前导字符（Q251：排除 %s 残留）
		if i := strings.LastIndex(c, "."); i >= 0 {
			// Q252 补：CTE 引用列（递归分支 WHERE w.d < ? 的 w 是
			// walk 别名）不当真实表列——启发式主查询是 CTE 时
			// AST 降级，此路径兜底
			if cte[c[:i]] {
				continue
			}
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

// stripSubqueries 剥离括号子查询块（P2a）：`(...)` 且括号内容以
// SELECT/WITH/VALUES 开头（忽略空白）→ 整块替换为等长空白——子查询
// 内部列名/占位符属于子查询作用域，不得混入外层 WHERE 过滤列；占位符
// 数量同步减少（? 顺序映射保持对齐）。普通括号（函数调用等）原样保留。
func stripSubqueries(s string) string {
	var out strings.Builder
	depth := 0
	start := -1
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			if depth == 0 {
				start = i
			}
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
			if depth == 0 && start >= 0 {
				trim := strings.TrimSpace(s[start+1 : i])
				u := strings.ToUpper(trim)
				if strings.HasPrefix(u, "SELECT") || strings.HasPrefix(u, "WITH") ||
					strings.HasPrefix(u, "VALUES") {
					out.WriteString(strings.Repeat(" ", i-start+1))
				} else {
					out.WriteString(s[start : i+1])
				}
				start = -1
			}
		default:
			if depth == 0 && start < 0 {
				out.WriteByte(s[i])
			}
		}
	}
	return out.String()
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

// reCTEAlias JOIN/FROM 段的 CTE 引用别名（Q252：`JOIN reach r`——
// WHERE r.d 的 r 是 CTE 别名，d 不得当真实表列）。
var reCTEAlias = regexp.MustCompile(`(?i)\b(?:JOIN|FROM)\s+([A-Za-z_][A-Za-z0-9_]*)\s+([A-Za-z_][A-Za-z0-9_]*)`)

// parseSQLStmt 混合解析（Q252）：vitess 专业解析器 AST 主路径——
// 完整 SQL 精确提取（词边界/列段/CTE 作用域/语句类型是解析器本职）；
// parse error（动态 SQL %s 残留、SQLite 特有语法 INSERT OR REPLACE/
// GLOB）降级启发式（残缺 SQL 尽量提取，保守不误报）。
func parseSQLStmt(sql string) (table, alias string, cols []string, whereCols []string, joinPairs []sqlJoinPair) {
	if t, a, c, wc, jp, ok := parseSQLStmtAST(sql); ok {
		sqlAstOK.Add(1) // R6：降级可观测埋点
		return t, a, c, wc, jp
	}
	// Q252 补：Go 反引号 SQL 不做转义——`\n\t` 是字面反斜杠，vitess
	// 视为非法 token；转真实空白后 AST 可解析（GetGrpcCalls 真实形态）
	if strings.Contains(sql, "\\n") || strings.Contains(sql, "\\t") {
		esc := strings.NewReplacer("\\n", "\n", "\\t", "\t").Replace(sql)
		if t, a, c, wc, jp, ok := parseSQLStmtAST(esc); ok {
			sqlAstOK.Add(1)
			return t, a, c, wc, jp
		}
	}
	sqlAstFail.Add(1) // R6：AST 失败（含转义第二尝试）
	sqlHeuristic.Add(1)
	return parseSQLStmtHeuristic(sql)
}
