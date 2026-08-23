package ssa

import "strings"

// parseSQLStmtHeuristic 启发式降级路径（Q97 起逐例修补的形态解析；
// Q252 后只服务 AST 覆盖不了的 SQL——动态拼接残留/SQLite 方言）。
func parseSQLStmtHeuristic(sql string) (table, alias string, cols []string, whereCols []string, joinPairs []sqlJoinPair) {
	upper := strings.ToUpper(sql)

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

	table = strings.TrimRight(table, ")")

	if cte[table] {
		return "", "", nil, nil, nil
	}
	if table == "" {
		return "", "", nil, nil, nil
	}

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
			alias = ""
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

	cteQual := map[string]bool{}
	for n := range cte {
		cteQual[n] = true
	}
	for _, m := range reCTEAlias.FindAllStringSubmatch(rest, -1) {
		if cte[m[1]] && m[2] != "" {
			cteQual[m[2]] = true
		}
	}
	whereCols = extractWhereCols(rest, cteQual)
	return table, alias, cols, whereCols, joinPairs
}
