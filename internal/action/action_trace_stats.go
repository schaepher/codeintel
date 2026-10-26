package action

// todo13-17 / todo14-17：value-trace 的预算/取消、分阶段真实计数与有界缓存。
//
//   - SQL 遍历用 QueryContext（ctx 取消会真正停止递归 CTE）
//   - 阶段计数都是**实际发生次数**（GetSymbol/GetLatest/CTE/派发），不把
//     结果行数或入口请求数当底层次数；覆盖/未覆盖范围显式输出
//   - 缓存键含索引 build_id + 请求参数；同索引容量有上限（FIFO 淘汰）

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// ErrTraceCanceled value-trace 被取消/超过预算（调用方不得当成功）。
var ErrTraceCanceled = errors.New("value-trace 已取消")

// vtCacheDefaultLimit value-trace 缓存条目上限（同索引不同请求时 FIFO 淘汰）。
const vtCacheDefaultLimit = 128

// TraceStats value-trace 的阶段计数（诊断输出；不影响语义）。
type TraceStats struct {
	SQLMS   float64 `json:"sql_ms,omitempty"`   // 数据库查询耗时（不含符号/条件/派发）
	SQLQueries int  `json:"sql_queries,omitempty"` // 实际 DB 往返（GetLatest + CTE）
	SymbolMS      float64 `json:"symbol_ms,omitempty"`
	SymbolLookups int     `json:"symbol_lookups,omitempty"` // 实际 GetSymbol 调用数
	ConditionMS   float64 `json:"condition_ms,omitempty"`
	ConditionRows int     `json:"condition_rows,omitempty"` // 实际做条件标注的行数
	DispatchMS    float64 `json:"dispatch_ms,omitempty"`
	DispatchCalls int     `json:"dispatch_calls,omitempty"` // GetDispatchTargets 实际调用数
	CacheHits     int     `json:"cache_hits,omitempty"`
	CacheMisses   int     `json:"cache_misses,omitempty"`
	CacheEntries  int     `json:"cache_entries,omitempty"`
	CacheEvictions int    `json:"cache_evictions,omitempty"`
	CoveredOps    []string `json:"covered_ops,omitempty"`
	UncoveredOps  []string `json:"uncovered_ops,omitempty"`
}

// traceCtxExpired ctx 是否已到期（除 Err 外直接比较 deadline——1µs 级
// 预算不依赖定时器调度时机）。
func traceCtxExpired(ctx context.Context) bool {
	if ctx.Err() != nil {
		return true
	}
	if dl, ok := ctx.Deadline(); ok && !time.Now().Before(dl) {
		return true
	}
	return false
}

// traceStageHook 测试钩子：阶段边界（before_sql/after_sql/after_conditions/
// after_dispatch）——用于确定性验证预算在任一阶段及时停止。
var traceStageHook func(phase string)

// traceStage 阶段边界检查：钩子（测试）→ ctx 是否过期。
func traceStage(ctx context.Context, phase string) bool {
	if traceStageHook != nil {
		traceStageHook(phase)
	}
	return traceCtxExpired(ctx)
}

// ValueTraceWithContext 可取消的 value-trace（含阶段统计/有界缓存）。
func (a *Actions) ValueTraceWithContext(ctx context.Context, nodeID domain.CanonicalID,
	maxDepth int, minConf float64, includeContainer bool) ([]*domain.TraceRow, *TraceStats, error) {
	logger := zap.L()
	logger.Info("enter (Actions).ValueTraceWithContext", zap.String("node_id", string(nodeID)))
	defer logger.Info("exit (Actions).ValueTraceWithContext")
	st := &TraceStats{
		CoveredOps: []string{"get_latest", "value_trace_cte", "symbol_lookup",
			"condition_annotation", "dispatch_targets"},
		UncoveredOps: []string{"adapter 内部 SQL/SSA 预计算", "渲染/源码片段读取"},
	}
	if traceStage(ctx, "before_sql") {
		return nil, st, ErrTraceCanceled
	}
	build := ""
	sqlStart := time.Now()
	if m, err := a.repo.GetLatest(); err == nil && m != nil {
		build = m.BuildID
	}
	st.SQLMS += float64(time.Since(sqlStart).Microseconds()) / 1000
	st.SQLQueries++
	key := fmt.Sprintf("%s|%s|%d|%.3f|%v", build, nodeID, maxDepth, minConf, includeContainer)
	if rows, ok := a.vtCacheGet(build, key, st); ok {
		return rows, st, nil
	}
	st.CacheMisses = 1

	if traceStage(ctx, "before_sql") {
		return nil, st, ErrTraceCanceled
	}
	start := time.Now()
	var rows []*domain.TraceRow
	var err error
	if c, ok := a.repo.(interface {
		GetValueTraceContext(context.Context, domain.CanonicalID, int, float64, bool) ([]*domain.TraceRow, error)
	}); ok {
		rows, err = c.GetValueTraceContext(ctx, nodeID, maxDepth, minConf, includeContainer)
	} else {
		rows, err = a.repo.GetValueTrace(nodeID, maxDepth, minConf, includeContainer)
	}
	st.SQLMS += float64(time.Since(start).Microseconds()) / 1000
	st.SQLQueries++
	if err != nil {
		if traceCtxExpired(ctx) {
			return nil, st, ErrTraceCanceled
		}
		return nil, st, err
	}
	if traceStage(ctx, "after_sql") {
		return nil, st, ErrTraceCanceled
	}
	// 条件标注（每行检查 ctx；符号查询/条件解析分别计数）
	rows, cs, err := a.TraceConditionsContext(ctx, rows)
	if err != nil {
		return nil, st, err
	}
	st.SymbolMS = cs.SymbolMS
	st.SymbolLookups = cs.SymbolLookups
	st.ConditionMS = cs.ConditionMS
	st.ConditionRows = cs.ConditionRows
	if traceStage(ctx, "after_conditions") {
		return nil, st, ErrTraceCanceled
	}
	// 接口派发候选标注（每行检查 ctx）
	start = time.Now()
	rows, err = a.markDispatchCandidatesContext(ctx, rows)
	st.DispatchMS = float64(time.Since(start).Microseconds()) / 1000
	st.DispatchCalls = 1
	if err != nil {
		if errors.Is(err, ErrTraceCanceled) {
			return nil, st, ErrTraceCanceled
		}
		return nil, st, err
	}
	if traceStage(ctx, "after_dispatch") {
		return nil, st, ErrTraceCanceled
	}
	a.vtCachePut(build, key, rows, st)
	return rows, st, nil
}

// vtCacheGet 命中返回行的拷贝；build 变化清空旧缓存。
func (a *Actions) vtCacheGet(build, key string, st *TraceStats) ([]*domain.TraceRow, bool) {
	a.vtMu.Lock()
	defer a.vtMu.Unlock()
	if a.vtBuild != build {
		a.vtCache = nil
		a.vtOrder = nil
		a.vtBuild = build
	}
	rows, ok := a.vtCache[key]
	st.CacheEntries = len(a.vtCache)
	st.CacheEvictions = a.vtEvictions
	if !ok {
		return nil, false
	}
	st.CacheHits = 1
	out := make([]*domain.TraceRow, len(rows))
	copy(out, rows)
	return out, true
}

// vtCachePut 写入缓存；超过上限按 FIFO 淘汰最旧请求键。
func (a *Actions) vtCachePut(build, key string, rows []*domain.TraceRow, st *TraceStats) {
	a.vtMu.Lock()
	defer a.vtMu.Unlock()
	if a.vtBuild != build {
		a.vtCache = nil
		a.vtOrder = nil
		a.vtBuild = build
	}
	if a.vtCache == nil {
		a.vtCache = map[string][]*domain.TraceRow{}
	}
	limit := a.vtCacheLimit
	if limit <= 0 {
		limit = vtCacheDefaultLimit
	}
	if _, exists := a.vtCache[key]; !exists {
		for len(a.vtCache) >= limit && len(a.vtOrder) > 0 {
			old := a.vtOrder[0]
			a.vtOrder = a.vtOrder[1:]
			if _, ok := a.vtCache[old]; ok {
				delete(a.vtCache, old)
				a.vtEvictions++
			}
		}
		a.vtOrder = append(a.vtOrder, key)
	}
	cp := make([]*domain.TraceRow, len(rows))
	copy(cp, rows)
	a.vtCache[key] = cp
	st.CacheEntries = len(a.vtCache)
	st.CacheEvictions = a.vtEvictions
}

// TraceConditionStats 条件标注阶段的真实计数。
type TraceConditionStats struct {
	SymbolLookups int
	SymbolMS      float64
	ConditionRows int
	ConditionMS   float64
}
