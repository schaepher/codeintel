package ssa

import (
	"strings"

	"golang.org/x/tools/go/ssa"
)

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
func whereColsOf(where string) []string {
	var cols []string
	up := strings.ToUpper(where)

	for _, stop := range []string{" ORDER BY ", " GROUP BY ", " LIMIT ", " OFFSET ", " HAVING "} {
		if j := strings.Index(up, stop); j >= 0 {
			where = where[:j]
			up = strings.ToUpper(where)

		}
	}

	for _, part := range whereCondRe.Split(where, -1) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if m := whereColLeadRe.FindStringSubmatch(part); m != nil {
			cols = append(cols, m[1])
			continue
		}

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
		return false
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

// sqlColUnqual 列别名归一（#249）：`e.source_id` 且 e 是主表别名 →
// source_id（前缀必须是 table 或 alias，否则丢弃返回空）。
func sqlColUnqual(table, alias, col string) string {
	if i := strings.Index(col, "."); i >= 0 {
		pre, rest := col[:i], col[i+1:]
		if pre == table || (alias != "" && pre == alias) {
			return rest
		}
		return ""
	}
	return col
}
