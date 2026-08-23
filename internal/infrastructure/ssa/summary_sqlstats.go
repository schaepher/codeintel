package ssa

import "sync/atomic"

// ---- R6 降级可观测：SQL 解析路径计数器 ----
// parseSQLStmt 的降级长期静默（R4：AST TableName 断言错误导致
// 全部 SQL 降级启发式，半年无人察觉）。计数器让每次构建暴露
// AST 成功/失败/启发式次数——"一直降级"提前可见。

var (
	sqlAstOK       atomic.Int64 // AST 主路径成功
	sqlAstFail     atomic.Int64 // AST 解析失败（含转义第二尝试失败）
	sqlHeuristic   atomic.Int64 // 最终走启发式
)

// ResetSQLStats 构建开始前清零。
func ResetSQLStats() {
	sqlAstOK.Store(0)
	sqlAstFail.Store(0)
	sqlHeuristic.Store(0)
}

// SQLStatsSnapshot 构建期降级统计快照。
type SQLStatsSnapshot struct {
	AstOK      int64 `json:"sql_ast_ok"`   // AST 主路径成功
	AstFail    int64 `json:"sql_ast_fail"` // AST 解析失败
	Heuristic  int64 `json:"sql_heuristic"` // 最终走启发式
}

// SQLStats 当前统计（R6：构建报告/查询展示）。
func SQLStats() SQLStatsSnapshot {
	return SQLStatsSnapshot{
		AstOK: sqlAstOK.Load(), AstFail: sqlAstFail.Load(), Heuristic: sqlHeuristic.Load(),
	}
}
