package action

// U04：持久化边界诊断——SQL 字面量 INSERT/UPDATE 的目标表与列写进调用节点
// 的诊断里；值来源/跨入口顺序仍需请求证明（不凭空连线）。
//
// 只识别**完整字面量**（含拼接后的静态串）；动态 SQL 保持原诊断。

import (
	"go/ast"
	"strings"
)

// seqPersistenceBoundary 调用的持久化边界描述（非持久化调用返回空串）。
func seqPersistenceBoundary(call *ast.CallExpr) string {
	if call == nil || len(call.Args) == 0 {
		return ""
	}
	sql := ""
	for _, a := range call.Args {
		if lit, ok := a.(*ast.BasicLit); ok && lit.Kind.String() == "STRING" {
			sql = strings.Trim(lit.Value, "`\"")
			break
		}
	}
	if sql == "" {
		return ""
	}
	upper := strings.ToUpper(strings.TrimSpace(sql))
	switch {
	case strings.HasPrefix(upper, "INSERT INTO"):
		if tbl, cols, ok := parseInsertTarget(sql); ok {
			return "持久化边界：INSERT INTO " + tbl + "(" + strings.Join(cols, ",") + ")（参数值来源未证明；跨入口顺序需请求身份证明）"
		}
		return "持久化边界：INSERT（表/列未静态可读）"
	case strings.HasPrefix(upper, "UPDATE"):
		if tbl, cols, ok := parseUpdateTarget(sql); ok {
			return "持久化边界：UPDATE " + tbl + " SET " + strings.Join(cols, ",") + "（值来源未证明）"
		}
		return "持久化边界：UPDATE（表/列未静态可读）"
	}
	return ""
}

// parseInsertTarget `INSERT INTO t (a,b) VALUES ...` → (t, [a,b])。
func parseInsertTarget(sql string) (string, []string, bool) {
	s := strings.TrimSpace(sql)
	up := strings.ToUpper(s)
	i := strings.Index(up, "INSERT INTO")
	if i < 0 {
		return "", nil, false
	}
	s = strings.TrimSpace(s[i+len("INSERT INTO"):])
	end := strings.IndexAny(s, "( \t\n")
	if end < 0 {
		return "", nil, false
	}
	table := s[:end]
	rest := strings.TrimSpace(s[end:])
	if !strings.HasPrefix(rest, "(") {
		return table, nil, table != ""
	}
	j := strings.Index(rest, ")")
	if j < 0 {
		return "", nil, false
	}
	cols := splitTrim(rest[1:j])
	return table, cols, table != ""
}

// parseUpdateTarget `UPDATE t SET a=?,b=? ...` → (t, [a,b])。
func parseUpdateTarget(sql string) (string, []string, bool) {
	s := strings.TrimSpace(sql)
	up := strings.ToUpper(s)
	if !strings.HasPrefix(up, "UPDATE") {
		return "", nil, false
	}
	s = strings.TrimSpace(s[len("UPDATE"):])
	end := strings.IndexAny(s, " \t\n")
	if end < 0 {
		return "", nil, false
	}
	table := s[:end]
	rest := strings.TrimSpace(s[end:])
	upRest := strings.ToUpper(rest)
	if !strings.HasPrefix(upRest, "SET") {
		return table, nil, table != ""
	}
	rest = strings.TrimSpace(rest[len("SET"):])
	if k := strings.Index(upRest, "WHERE"); k >= 0 {
		_ = k
	}
	if k := strings.Index(strings.ToUpper(rest), "WHERE"); k >= 0 {
		rest = rest[:k]
	}
	var cols []string
	for _, part := range splitTrim(rest) {
		if eq := strings.Index(part, "="); eq > 0 {
			cols = append(cols, strings.TrimSpace(part[:eq]))
		}
	}
	return table, cols, table != ""
}

func splitTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// seqAnnotatePersistence 给调用节点补持久化边界诊断（不覆盖既有 code）。
func seqAnnotatePersistence(node *CodeSeqNode, call *ast.CallExpr) {
	if node == nil {
		return
	}
	detail := seqPersistenceBoundary(call)
	if detail == "" {
		return
	}
	if node.Stop == nil {
		node.Stop = &SeqStop{Code: SeqStopIndexGap, Detail: detail}
		return
	}
	node.Stop.Detail = detail + "；" + node.Stop.Detail
}
