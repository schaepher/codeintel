package action

// todo13-17：value-trace 的预算/取消、阶段计数与结果缓存。
//
//   - SQL 遍历用 QueryContext（ctx 取消会真正停止递归 CTE）
//   - 阶段计数：SQL 次数/耗时、符号查询次数/耗时、缓存命中/未命中
//   - 缓存键含索引 build_id：索引身份变化后旧正/负缓存不再命中

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

// TraceStats value-trace 的阶段计数（诊断输出；不影响语义）。
type TraceStats struct {
	SQLMS      float64 `json:"sql_ms,omitempty"`
	SQLQueries int     `json:"sql_queries,omitempty"`
	SymbolMS      float64 `json:"symbol_ms,omitempty"`
	SymbolLookups int     `json:"symbol_lookups,omitempty"`
	CacheHits     int     `json:"cache_hits,omitempty"`
	CacheMisses   int     `json:"cache_misses,omitempty"`
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

// ValueTraceWithContext 可取消的 value-trace（含阶段统计/缓存）。
func (a *Actions) ValueTraceWithContext(ctx context.Context, nodeID domain.CanonicalID,
	maxDepth int, minConf float64, includeContainer bool) ([]*domain.TraceRow, *TraceStats, error) {
	logger := zap.L()
	logger.Info("enter (Actions).ValueTraceWithContext", zap.String("node_id", string(nodeID)))
	defer logger.Info("exit (Actions).ValueTraceWithContext")
	st := &TraceStats{}
	if traceCtxExpired(ctx) {
		return nil, st, ErrTraceCanceled
	}
	build := ""
	if m, err := a.repo.GetLatest(); err == nil && m != nil {
		build = m.BuildID
	}
	key := fmt.Sprintf("%s|%s|%d|%.3f|%v", build, nodeID, maxDepth, minConf, includeContainer)
	a.vtMu.Lock()
	if a.vtBuild != build {
		a.vtCache = nil
		a.vtBuild = build
	}
	if rows, ok := a.vtCache[key]; ok {
		a.vtMu.Unlock()
		st.CacheHits = 1
		out := make([]*domain.TraceRow, len(rows))
		copy(out, rows)
		return out, st, nil
	}
	a.vtMu.Unlock()
	st.CacheMisses = 1

	if traceCtxExpired(ctx) {
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
	st.SQLMS = float64(time.Since(start).Microseconds()) / 1000
	st.SQLQueries = 1
	if err != nil {
		if traceCtxExpired(ctx) {
			return nil, st, ErrTraceCanceled
		}
		return nil, st, err
	}
	// 条件标注（符号查询 + 源码条件；单独计时）
	start = time.Now()
	rows, err = a.TraceConditions(rows)
	st.SymbolMS = float64(time.Since(start).Microseconds()) / 1000
	st.SymbolLookups = len(rows)
	if err != nil {
		return nil, st, err
	}
	if traceCtxExpired(ctx) {
		return nil, st, ErrTraceCanceled
	}
	rows, err = a.markDispatchCandidates(rows)
	if err != nil {
		return nil, st, err
	}
	// 缓存（拷贝切片；行内容只读）
	a.vtMu.Lock()
	if a.vtBuild == build {
		if a.vtCache == nil {
			a.vtCache = map[string][]*domain.TraceRow{}
		}
		cp := make([]*domain.TraceRow, len(rows))
		copy(cp, rows)
		a.vtCache[key] = cp
	}
	a.vtMu.Unlock()
	return rows, st, nil
}
