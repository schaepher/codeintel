package ssa

import (
	"regexp"
)




// whereColsOf 从 where 条件串提取列名：AND/OR 拆分 + 占位符剥离
// （IN (?) 先处理；其余形态截到最后一个 ? 再 TrimRight 运算符——
// 兼容 " = ?" / "=?" / " <?" / " LIKE ?" 等有无空格写法，以及多行
// 条件串（AND/OR 前后为换行/制表符——pay_order 实测整串未被拆分）。

// whereCondRe AND/OR 条件拆分（大小写不敏感，\s 覆盖换行/制表符）。
var whereCondRe = regexp.MustCompile(`(?i)\s+(AND|OR)\s+`)

// whereColLeadRe 条件串首列名提取：列名 + 操作符（= / <> / < / > /
// LIKE / BETWEEN / IS / IN），操作符后须接空白或占位符/数字（兼容
// "ad_id=$1" 无空格、"? " 与 "0" 字面量）。列名支持表前缀（b.id）。
var whereColLeadRe = regexp.MustCompile(`(?i)^([A-Za-z_][A-Za-z0-9_.]*)\s*(?:=|<>|<=|>=|<|>|LIKE|BETWEEN|IS|IN)(?:\s|[$\?0-9])`)


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

