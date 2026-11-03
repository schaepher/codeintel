package action

// U04：持久化边界诊断——SQL 字面量 INSERT/UPDATE 的目标表与列写进调用节点
// 的诊断里；值来源/跨入口顺序仍需请求证明（不凭空连线）。
//
// U04a：只有**准确的 database/sql 写入方法身份**（DB/Tx Exec、
// DB/Tx/Conn ExecContext）才解析 SQL——自定义同名方法、日志调用、
// Stmt.Exec 都不是写入。
//
// U04b：表/列来自 SQL AST（`action_seq_storage.go`），不再按逗号/字符串
// 切分；复杂/动态 SQL 不保留看起来确定的部分表列。

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"
)

// 标准库写入方法的 SQL 实参位置（U04a）：Exec(query, args...) 的 query 是
// 参数 0；ExecContext(ctx, query, args...) 的 query 是参数 1。
const (
	seqSQLMethodExec          = "symbol:go:database/sql:(DB).Exec"
	seqSQLMethodTxExec        = "symbol:go:database/sql:(Tx).Exec"
	seqSQLMethodDBExecContext = "symbol:go:database/sql:(DB).ExecContext"
	seqSQLMethodTxExecCtx     = "symbol:go:database/sql:(Tx).ExecContext"
	seqSQLMethodConnExecCtx   = "symbol:go:database/sql:(Conn).ExecContext"
)

// seqPersistenceSQLArg 调用点是否为**已证明的 database/sql 写入调用**；
// 是则返回 SQL 实参索引，-1 表示不是写入（自定义同名方法、Stmt.Exec、
// 日志/未解析调用都不算）。
func seqPersistenceSQLArg(node *CodeSeqNode) int {
	if node == nil || node.Evidence == nil {
		return -1
	}
	switch node.Evidence.SymbolID {
	case seqSQLMethodExec, seqSQLMethodTxExec:
		return 0
	case seqSQLMethodDBExecContext, seqSQLMethodTxExecCtx, seqSQLMethodConnExecCtx:
		return 1
	}
	return -1
}

// seqPersistenceSQLText 取出 SQL 字面量（Go 字符串语法解码）；未知查询、
// 非字面量、ellipsis 实参一律未证明（返回空串）。
func seqPersistenceSQLText(call *ast.CallExpr, arg int) string {
	if call == nil || arg < 0 || arg >= len(call.Args) {
		return ""
	}
	if call.Ellipsis.IsValid() && arg == len(call.Args)-1 {
		return ""
	}
	lit, ok := call.Args[arg].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	if v, err := strconv.Unquote(lit.Value); err == nil {
		return v
	}
	return ""
}

// seqPersistenceBoundary 调用的持久化边界描述（非持久化调用返回空串）。
// 表/列来自 SQL AST；形态未证明时只说操作名（不猜表列）。
func seqPersistenceBoundary(node *CodeSeqNode, call *ast.CallExpr) string {
	arg := seqPersistenceSQLArg(node)
	if arg < 0 {
		return ""
	}
	sql := seqPersistenceSQLText(call, arg)
	if sql == "" {
		return ""
	}
	b := &seqSQLBinder{lenient: true}
	if len(call.Args) > arg+1 {
		b.args = call.Args[arg+1:]
	}
	st, code := seqStorageParse(sql, b)
	if st != nil {
		switch st.Operation {
		case SeqStorageInsert:
			cols := seqStorageWriteColumns(st.Writes)
			return "持久化边界：INSERT INTO " + st.Table + "(" + strings.Join(cols, ",") + ")（参数值来源未证明；跨入口顺序需请求身份证明）"
		case SeqStorageUpdate:
			cols := seqStorageWriteColumns(st.Writes)
			return "持久化边界：UPDATE " + st.Table + " SET " + strings.Join(cols, ",") + "（值来源未证明）"
		}
	}
	_ = code
	return "持久化边界：" + seqStorageOperationGuess(sql) + "（表/列未静态可读）"
}

// seqStorageWriteColumns 写入列名（诊断文本用）。
func seqStorageWriteColumns(writes []SeqStorageWrite) []string {
	out := make([]string, 0, len(writes))
	for _, w := range writes {
		out = append(out, w.Column)
	}
	return out
}

// seqStorageOperationGuess 从 SQL 前缀猜操作名（仅用于诊断文本，不用于证据）。
func seqStorageOperationGuess(sql string) string {
	upper := strings.ToUpper(strings.TrimSpace(sql))
	switch {
	case strings.HasPrefix(upper, "INSERT"):
		return "INSERT"
	case strings.HasPrefix(upper, "UPDATE"):
		return "UPDATE"
	}
	return "SQL"
}

// seqAnnotatePersistence 给调用节点补持久化边界诊断（不覆盖既有 code）。
func seqAnnotatePersistence(node *CodeSeqNode, call *ast.CallExpr) {
	if node == nil {
		return
	}
	detail := seqPersistenceBoundary(node, call)
	if detail == "" {
		return
	}
	if node.Stop == nil {
		node.Stop = &SeqStop{Code: SeqStopIndexGap, Detail: detail}
		return
	}
	node.Stop.Detail = detail + "；" + node.Stop.Detail
}
