package ssa

import (
	"strings"
	"sync"
	"sync/atomic"
)

// ---- R6 降级可观测：SQL 解析路径计数器 ----
// parseSQLStmt 的降级长期静默（R4：AST TableName 断言错误导致
// 全部 SQL 降级启发式，半年无人察觉）。计数器让每次构建暴露
// AST 成功/失败/启发式次数——"一直降级"提前可见。

var (
	sqlAstOK       atomic.Int64 // AST 主路径成功
	sqlAstFail     atomic.Int64 // AST 解析失败（含转义第二尝试失败）
	sqlFailed      sync.Map     // 降级 SQL 去重集（同一 SQL 只计一次——多候选重复计数失真）
)

// classifyHeuristic 降级形态分类（R7 调查）：动态拼接（%s 等 Sprintf
// 残留）与 SQLite 方言是预期降级；其他形态（非预期）值得关注。
// 去重：同一 SQL 文本只计一次（动态 SQL 多候选解析会重复计数——
// R7 实测 37 次失败实为 5 条动态 SQL 的候选变体）。
func classifyHeuristic(sql string) {
	key := strings.TrimSpace(sql)
	if _, loaded := sqlFailed.LoadOrStore(key, true); loaded {
		return
	}
	up := strings.ToUpper(key)
	switch {
	case strings.Contains(sql, "%") && !strings.Contains(sql, "%%"):
		// 动态拼接：预期降级
	case strings.HasPrefix(up, "WITH") && strings.Contains(up, "RECURSIVE"):
		// 递归 CTE 应 AST 支持——失败则需查
	case strings.Contains(up, "GLOB") || strings.HasPrefix(up, "INSERT OR"):
		// SQLite 方言：预期降级
	default:
	}
}

// ResetSQLStats 构建开始前清零。
func ResetSQLStats() {
	sqlAstOK.Store(0)
	sqlAstFail.Store(0)
	sqlFailed = sync.Map{}
}

// SQLStatsSnapshot 构建期降级统计快照。
type SQLStatsSnapshot struct {
	AstOK       int64 `json:"sql_ast_ok"`    // AST 主路径成功
	AstFail     int64 `json:"sql_ast_fail"`  // AST 解析失败
	Heuristic   int64 `json:"sql_heuristic"` // 最终走启发式（去重后 SQL 条数）
	HeurDynamic int64 `json:"heur_dynamic"`  // 降级形态：动态拼接（% 残留，预期）
	HeurDialect int64 `json:"heur_dialect"`  // 降级形态：SQLite 方言（GLOB/INSERT OR）
	HeurOther   int64 `json:"heur_other"`    // 降级形态：其他（非预期，值得关注）
}

// SQLStats 当前统计（R6：构建报告/查询展示）。
func SQLStats() SQLStatsSnapshot {
	// 去重统计：遍历失败 SQL 集（并发安全——快照语义）
	var dyn, dial, other int64
	sqlFailed.Range(func(k, _ any) bool {
		up := strings.ToUpper(strings.TrimSpace(k.(string)))
		switch {
		case strings.Contains(k.(string), "%") && !strings.Contains(k.(string), "%%"):
			dyn++
		case strings.HasPrefix(up, "WITH") && strings.Contains(up, "RECURSIVE"):
			other++
		case strings.Contains(up, "GLOB") || strings.HasPrefix(up, "INSERT OR"):
			dial++
		default:
			other++
		}
		return true
	})
	total := dyn + dial + other
	return SQLStatsSnapshot{
		AstOK: sqlAstOK.Load(), AstFail: sqlAstFail.Load(), Heuristic: total,
		HeurDynamic: dyn, HeurDialect: dial, HeurOther: other,
	}
}
