package sqlite

// todo21：CALLS 边遍历（callers/callees）——一跳直接邻接查询、多跳递归
// （物化 CTE 以支持取消）。从 repo_graph.go 拆出（行数治理）。

import (
	"context"
	"fmt"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// GetCallers 返回调用 id（或更上层）的边，深度 ≤ depth，置信度 ≥ minConfidence。
// 递归 CTE 沿 source 方向向上遍历（TD.md ImpactAnalysisSpecification）。
// 兼容入口：无请求上下文（后台执行）；需要可取消时用 GetCallersContext。
func (r *Repo) GetCallers(id domain.CanonicalID, depth int, minConfidence float64) ([]*domain.Fact, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetCallers")
	defer logger.Debug("exit (Repo).GetCallers")
	return r.GetCallersContext(context.Background(), id, depth, minConfidence)
}

// GetCallersContext 同 GetCallers，查询可被 ctx 取消（连接等待与 SQL 执行）。
func (r *Repo) GetCallersContext(ctx context.Context, id domain.CanonicalID, depth int, minConfidence float64) ([]*domain.Fact, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetCallersContext")
	defer logger.Debug("exit (Repo).GetCallersContext")
	return r.walkEdges(ctx, string(id), depth, minConfidence, "callers")
}

// GetCallees 返回 id 调用（或更下层）的边，深度 ≤ depth，置信度 ≥ minConfidence。
// 兼容入口：无请求上下文（后台执行）；需要可取消时用 GetCalleesContext。
func (r *Repo) GetCallees(id domain.CanonicalID, depth int, minConfidence float64) ([]*domain.Fact, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetCallees")
	defer logger.Debug("exit (Repo).GetCallees")
	return r.GetCalleesContext(context.Background(), id, depth, minConfidence)
}

// GetCalleesContext 同 GetCallees，查询可被 ctx 取消（连接等待与 SQL 执行）。
func (r *Repo) GetCalleesContext(ctx context.Context, id domain.CanonicalID, depth int, minConfidence float64) ([]*domain.Fact, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetCalleesContext")
	defer logger.Debug("exit (Repo).GetCalleesContext")
	return r.walkEdges(ctx, string(id), depth, minConfidence, "callees")
}

// walkEdges 沿单向方向遍历 CALLS 边。
//
//	callers: edges 从 id 向上（e.target_id 为已到达节点）
//	callees: edges 从 id 向下（e.source_id 为已到达节点）
//
// todo21-14.3：depth<=1（含 0/负值——历史上同样只取一跳）不需要递归，走
// 直接邻接查询：旧递归 CTE 即使 depth=1 也要规划/执行递归体（真实索引
// 实测约 4.7–5.9s；直接查询同一批边约 4.8ms）。DISTINCT、六列结果、
// kind='calls'、置信度过滤全部保留。
//
// 注意（14.4）：modernc.org/sqlite v1.57.0 的 stmt.query 在返回首条结果后
// **撤销** ctx 的 interruptOnDone 监听（defer），此后 rows.Next 的 sqlite
// step 不在取消窗口内；深递归查询因此无法被 QueryContext 中断。递归体用
// AS MATERIALIZED 强制在首条结果返回前完成重计算，取消才真正生效
// （见 repo_packages_context_test.go 的 TestCalleesContextInterruptsRecursiveSQL）。
// 撤销条件：驱动版本把取消监听延长到 rows 生命周期内，且该回归仍通过。
func (r *Repo) walkEdges(ctx context.Context, id string, depth int, minConfidence float64, dir string) ([]*domain.Fact, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).walkEdges")
	defer logger.Debug("exit (Repo).walkEdges")
	var anchor, walkCol string
	if dir == "callers" {
		anchor, walkCol = "target_id", "src"
	} else {
		anchor, walkCol = "source_id", "tgt"
	}
	if depth <= 1 {
		rows, err := r.QueryContext(ctx, walkDirectQuery(anchor), id, minConfidence)
		if err != nil {
			return nil, fmt.Errorf("walk %s of %s: %w", dir, id, err)
		}
		defer rows.Close()
		return scanFacts(rows)
	}
	rows, err := r.QueryContext(ctx, walkRecursiveQuery(anchor, walkCol, true),
		id, minConfidence, depth, minConfidence)
	if err != nil {
		return nil, fmt.Errorf("walk %s of %s: %w", dir, id, err)
	}
	defer rows.Close()
	return scanFacts(rows)
}

// walkDirectQuery 一跳（含 depth<=0 的历史语义）的直接邻接查询 SQL。
// todo22-14.4-B：资源基准与生产共用同一句——基准不复制 SQL，避免
// "测的语句和生产跑的不是同一句"。
func walkDirectQuery(anchor string) string {
	return fmt.Sprintf(`
SELECT DISTINCT source_id, target_id, kind, tool_source, confidence, metadata
FROM edges_v WHERE %s = ? AND kind = 'calls' AND confidence >= ?`, anchor)
}

// walkRecursiveQuery 多跳递归 CTE（UNION + 外层 DISTINCT + 深度/置信度过滤）。
//
// materialized=true 是生产语句（取消语义依赖物化，见 walkEdges 注释）；
// false 是物化前的旧实现，仅用于兼容对照与资源基准的 recursive 组
// （todo22-14.4-B）——生产路径不得传 false。
func walkRecursiveQuery(anchor, walkCol string, materialized bool) string {
	cte := "AS ("
	if materialized {
		cte = "AS MATERIALIZED ("
	}
	return fmt.Sprintf(`
WITH RECURSIVE walk(src, tgt, kind, tool_source, confidence, metadata, d) %s
    SELECT source_id, target_id, kind, tool_source, confidence, metadata, 1
    FROM edges_v WHERE %s = ? AND kind = 'calls' AND confidence >= ?
    UNION
    SELECT e.source_id, e.target_id, e.kind, e.tool_source, e.confidence, e.metadata, w.d + 1
    FROM edges_v e JOIN walk w ON e.%s = w.%s
    WHERE w.d < ? AND e.kind = 'calls' AND e.confidence >= ?
)
SELECT DISTINCT src, tgt, kind, tool_source, confidence, metadata FROM walk`,
		cte, anchor, anchor, walkCol)
}
