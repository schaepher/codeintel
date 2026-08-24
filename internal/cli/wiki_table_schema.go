package cli

import (
	"strings"
)

// schemaCol 一个列的 schema 事实（R19：sqlite_master 解析）。
type schemaCol struct {
	Typ	string
	Def	string
	AutoInc	bool	// R22：INTEGER PRIMARY KEY（SQLite rowid 自增）
}
// parseCreateTableSchema 解析 CREATE TABLE DDL → 每表列类型/默认值
// （R19：不借助 AI 填类型——SQLite schema 是权威事实源）。
// 跳过 FOREIGN KEY/PRIMARY KEY/UNIQUE/CHECK 等约束行；列定义
// `name TYPE [NOT NULL] [DEFAULT x]`——取第一个 token 为类型，
// DEFAULT 后（去注释）为默认值。
func parseCreateTableSchema(ddls map[string]string) map[string]map[string]schemaCol {
	out := map[string]map[string]schemaCol{}
	for table, ddl := range ddls {
		cols := map[string]schemaCol{}
		for _, line := range strings.Split(ddl, "\n") {
			line = strings.TrimSpace(line)
			line = strings.TrimSuffix(line, ",")

			if strings.HasPrefix(line, ",") {
				line = strings.TrimSpace(strings.TrimPrefix(line, ","))
			}
			if line == "" || line == "(" || line == ")" {
				continue
			}
			up := strings.ToUpper(line)
			if strings.HasPrefix(up, "CREATE TABLE") || strings.HasPrefix(up, "FOREIGN KEY") ||
				strings.HasPrefix(up, "PRIMARY KEY") || strings.HasPrefix(up, "UNIQUE") ||
				strings.HasPrefix(up, "CHECK") || strings.HasPrefix(up, "CONSTRAINT") {
				continue
			}

			if i := strings.Index(line, "--"); i >= 0 {
				line = strings.TrimSpace(line[:i])
			}
			rawName := line
			if i := strings.IndexAny(line, " \t"); i >= 0 {
				rawName = line[:i]
			}

			name := strings.Trim(rawName, `"`+"`[]")
			rest := ""
			if len(line) > len(rawName)+1 {
				rest = strings.TrimSpace(line[len(rawName)+1:])
			}
			if name == "" {
				continue
			}
			if rest == "" {
				continue
			}
			c := schemaCol{}

			if strings.Contains(strings.ToUpper(rest), "PRIMARY KEY") && strings.HasPrefix(strings.ToUpper(rest), "INTEGER") {
				c.AutoInc = true
			}

			if i := strings.IndexAny(rest, " \t"); i >= 0 {
				c.Typ = rest[:i]
			} else {
				c.Typ = rest
			}
			c.Typ = strings.TrimSuffix(c.Typ, ",")

			if i := strings.Index(strings.ToUpper(rest), "DEFAULT"); i >= 0 {
				c.Def = strings.TrimSpace(rest[i+len("DEFAULT"):])
			}
			cols[name] = c
		}
		out[table] = cols
	}
	return out
}
