package ssa

import "strings"

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

	aliases := map[string]string{}
	firstTok, firstRest := nextToken(rest)
	if firstTok != "" {
		aliases[firstTok] = firstTok
		if a, _ := nextToken(firstRest); a != "" && !sqlKwdSet[strings.ToUpper(a)] && !strings.HasPrefix(a, "(") {
			aliases[a] = firstTok
		}
	}

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

		tbl, afterTbl := nextToken(seg)
		if tbl == "" {
			break
		}

		if cte[tbl] {
			rest = afterTbl
			up = strings.ToUpper(rest)
			continue
		}
		aliases[tbl] = tbl

		if a, afterAlias := nextToken(afterTbl); a != "" && !sqlKwdSet[strings.ToUpper(a)] {
			aliases[a] = tbl
			afterTbl = afterAlias
		}

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

		for _, stop := range []string{" INNER JOIN ", " LEFT JOIN ", " RIGHT JOIN ", " CROSS JOIN ",
			" FULL JOIN ", " OUTER JOIN ", " JOIN ", " WHERE ", " ORDER BY ", " LIMIT ",
			" GROUP BY ", " HAVING ", " UNION "} {
			if k := strings.Index(strings.ToUpper(onSeg), stop); k >= 0 && k < end {
				end = k
			}
		}
		onPart := onSeg[:end]
		// P2a：ON 内子查询作用域——EXISTS/IN (SELECT…) 内部的等值
		// 比较属子查询内部，不得误作 JOIN 键对
		onPart = stripSubqueries(onPart)
		for _, cond := range splitSQLCond(onPart) {
			eq := strings.Index(cond, "=")
			if eq < 0 {
				continue
			}
			l := strings.TrimSpace(cond[:eq])
			r := strings.TrimSpace(cond[eq+1:])

			if i := strings.IndexAny(l, " \t\n"); i >= 0 {
				l = l[:i]
			}
			if i := strings.IndexAny(r, " \t\n"); i >= 0 {
				r = r[:i]
			}
			lt, lc := splitQualified(l)
			rt, rc := splitQualified(r)

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

		if j+len(" JOIN ") >= len(rest) {
			break
		}
		rest = afterTbl
		up = strings.ToUpper(rest)
	}
	return pairs
}
