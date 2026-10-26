package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// queryValueTrace 输出数据值在整条链路上的处理过程，按函数上下文分组
// （field_trace.md §14.2 数据值全链追踪）。
func queryValueTrace(acts *action.Actions, nodeID string, maxDepth int, minConf float64, includeContainer bool, opts outputOpts, format string, timeout time.Duration) int {
	logger := zap.L()
	logger.Debug("enter queryValueTrace")
	defer logger.Debug("exit queryValueTrace")
	// todo12-10 R2：value-trace 的输入契约是**节点 ID**（写点/SSA 值节点），
	// 字段名锚点属于 query trace 语义——分开说明，避免静默空结果。
	if !strings.HasPrefix(nodeID, "symbol:") && !strings.HasPrefix(nodeID, "file:") && !strings.HasPrefix(nodeID, "commit:") {
		fmt.Fprintf(os.Stderr, "error: value-trace 需要节点 ID（写点/SSA 值节点）；字段名锚点请用 codeintel query trace <字段> --func <函数>\n")
		return 2
	}
	effectiveDepth := maxDepth
	if effectiveDepth <= 0 {
		effectiveDepth = 8
	}
	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	vtStart := time.Now()
	rows, stats, err := acts.ValueTraceWithContext(ctx, domain.CanonicalID(nodeID), maxDepth, minConf, includeContainer)
	if err != nil {
		if errors.Is(err, action.ErrTraceCanceled) {
			progress := ""
			if stats != nil {
				progress = fmt.Sprintf("；已执行 sql_queries=%d symbol_lookups=%d condition_rows=%d dispatch_calls=%d",
					stats.SQLQueries, stats.SymbolLookups, stats.ConditionRows, stats.DispatchCalls)
			}
			if timeout > 0 && ctx.Err() == context.DeadlineExceeded {
				fmt.Fprintf(os.Stderr, "error: value-trace 超过查询预算 %s——已停止后台遍历，结果不完整不输出%s\n", timeout, progress)
			} else {
				fmt.Fprintf(os.Stderr, "error: value-trace 已取消（未完成的遍历不作为结果输出）%s\n", progress)
			}
			return 1
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	analysis := valueTraceCompleteness(rows, effectiveDepth)
	if stats != nil {
		analysis.SQLMS, analysis.SQLQueries = stats.SQLMS, stats.SQLQueries
		analysis.SymbolMS, analysis.SymbolLookups = stats.SymbolMS, stats.SymbolLookups
		analysis.ConditionMS, analysis.ConditionRows = stats.ConditionMS, stats.ConditionRows
		analysis.DispatchMS, analysis.DispatchCalls = stats.DispatchMS, stats.DispatchCalls
		analysis.CacheHits, analysis.CacheMisses = stats.CacheHits, stats.CacheMisses
		analysis.CacheEntries, analysis.CacheEvictions = stats.CacheEntries, stats.CacheEvictions
		analysis.CoveredOps, analysis.UncoveredOps = stats.CoveredOps, stats.UncoveredOps
	}
	analysis.ElapsedMS = float64(time.Since(vtStart).Microseconds()) / 1000
	if opts.json {
		type flowRow struct {
			ID            string             `json:"id"`
			Name          string             `json:"name"`
			Depth         int                `json:"depth"`
			Dir           int                `json:"dir"`
			Edge          string             `json:"edge"`
			Line          int                `json:"line"`
			Kind          string             `json:"kind"`
			Access        string             `json:"access"`
			FuncID        string             `json:"func_id"`
			FuncName      string             `json:"func_name"`
			Conditions    []string           `json:"conditions,omitempty"`
			Dispatch      *dispatchJSON      `json:"dispatch,omitempty"`       // Q157 P1 候选标注
			EdgeCandidate *edgeCandidateJSON `json:"edge_candidate,omitempty"` // Q161 边级候选标注
		}
		jrows := make([]flowRow, 0, len(rows))
		for _, r := range rows {
			var disp *dispatchJSON
			if r.DispatchCandidate {
				disp = &dispatchJSON{Origin: r.DispatchOrigin, Confidence: r.DispatchConf}
			}
			var ec *edgeCandidateJSON
			if r.EdgeOrigin != "" {
				ec = &edgeCandidateJSON{Iface: r.EdgeIface, Origin: r.EdgeOrigin, Confidence: r.EdgeConf}
			}
			jrows = append(jrows, flowRow{ID: string(r.ID), Name: r.Name, Depth: r.Depth, Dir: r.Dir,
				Edge: lastEdgeKind(r.EdgeKinds), Line: r.Line, Kind: string(r.Kind), Access: r.Access,
				FuncID: r.FuncID, FuncName: shortFuncName(r.FuncID), Conditions: r.Conditions,
				Dispatch: disp, EdgeCandidate: ec})
		}
		encodeJSON(map[string]any{"flows": jrows, "analysis": analysis})
		return 0
	}
	if len(rows) == 0 {
		fmt.Printf("无数据流（节点不存在或无数据流边）；分析：%s（%s）\n", analysis.Status, analysis.Stop)
		return 0
	}
	if analysis.Status != "complete" {
		detail := analysis.Stop
		if analysis.StopDetail != "" {
			detail += "：" + analysis.StopDetail
		}
		if analysis.StopFile != "" || analysis.StopLine > 0 {
			detail += fmt.Sprintf(" @%s:%d", analysis.StopFile, analysis.StopLine)
		}
		fmt.Printf("分析：%s（%s；--max-depth %d）\n", analysis.Status, detail, effectiveDepth)
	}
	// Q235-10：四格式渲染（文本分组 / tree / mermaid / json 在前）
	if format == "" {
		format = "text"
	}
	repoDir := opts.repoPath
	if repoDir == "" {
		repoDir = "."
	}
	fmt.Print(vtRender(rows, repoDir, format))
	return 0
}

// shortFuncName 从函数 canonical ID 提取短名（symbol:go:<pkg>:<name> → <name>）。
func shortFuncName(id string) string {
	if i := strings.LastIndex(id, ":"); i >= 0 {
		return id[i+1:]
	}
	return id
}

// querySummary 跨层摘要（Q100）：字段生命周期主链
// （入口 → 计算 → 写入 → 消费），每步带 file:line。
func querySummary(acts *action.Actions, input string, opts outputOpts, format string) int {
	logger := zap.L()
	logger.Debug("enter querySummary")
	defer logger.Debug("exit querySummary")

	anchor, err := acts.ResolveAnchor(input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	steps, err := acts.SummaryChain(anchor)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if len(steps) == 0 {
		fmt.Println("无生命周期链路（节点不存在或无数据流边）")
		return 0
	}
	if opts.json {
		encodeJSON(map[string]any{"steps": steps})
		return 0
	}
	if format == "mermaid" {
		var sb strings.Builder
		sb.WriteString("flowchart LR\n")
		for _, st := range steps {
			label := st.Name
			if st.Line > 0 {
				label += fmt.Sprintf(":%d", st.Line)
			}
			sb.WriteString(fmt.Sprintf("    %q --> %q\n", st.Func+"|"+label, st.Kind+"|"+st.Name))
		}
		fmt.Println(sb.String())
		return 0
	}
	fmt.Printf("字段生命周期: %s（%d 步）\n", steps[0].Name, len(steps))
	for _, st := range steps {
		loc := ""
		if st.File != "" {
			loc = st.File
			if st.Line > 0 {
				loc += fmt.Sprintf(":%d", st.Line)
			}
		}
		fmt.Printf("  %-8s %-40s %s %s\n", "["+st.Kind+"]", st.Name, loc, st.Func)
	}
	return 0
}

// valueTraceAnalysis 值追踪完整性（todo12-10 R2）：深度截断/无流 → partial，
// 不把"只看到几跳"表现为完整分析。
type valueTraceCompletenessT struct {
	Status string `json:"status"` // complete|partial
	Depth  int    `json:"depth"`
	Stop   string `json:"stop,omitempty"` // depth|no_flow|unknown_callback
	Nodes  int    `json:"nodes"`
	// todo13-10：未知回调的停止说明与位置（非空且未达深度上限也不等于 complete）
	StopDetail string `json:"stop_detail,omitempty"`
	StopFile   string `json:"stop_file,omitempty"`
	StopLine   int    `json:"stop_line,omitempty"`
	// todo12-17 R1 / todo13-17：分阶段计数（SQL/符号）与缓存命中
	ElapsedMS     float64 `json:"elapsed_ms,omitempty"`
	SQLMS         float64 `json:"sql_ms,omitempty"`
	SQLQueries    int     `json:"sql_queries,omitempty"`
	SymbolMS      float64 `json:"symbol_ms,omitempty"`
	SymbolLookups int     `json:"symbol_lookups,omitempty"`
	ConditionMS   float64 `json:"condition_ms,omitempty"`
	ConditionRows int     `json:"condition_rows,omitempty"`
	DispatchMS    float64 `json:"dispatch_ms,omitempty"`
	DispatchCalls int     `json:"dispatch_calls,omitempty"`
	CacheHits     int     `json:"cache_hits,omitempty"`
	CacheMisses   int     `json:"cache_misses,omitempty"`
	CacheEntries  int     `json:"cache_entries,omitempty"`
	CacheEvictions int    `json:"cache_evictions,omitempty"`
	CoveredOps    []string `json:"covered_ops,omitempty"`
	UncoveredOps  []string `json:"uncovered_ops,omitempty"`
}

func valueTraceCompleteness(rows []*domain.TraceRow, depth int) valueTraceCompletenessT {
	out := valueTraceCompletenessT{Status: "complete", Depth: depth, Nodes: len(rows)}
	if len(rows) == 0 {
		out.Status, out.Stop = "partial", "no_flow"
		return out
	}
	// todo13-10：未知回调（来源未建模）→ partial，优先于 depth 说明具体原因
	for _, r := range rows {
		if r.UnknownCall != "" {
			out.Status, out.Stop = "partial", "unknown_callback"
			out.StopDetail = r.UnknownCallReason
			out.StopFile, out.StopLine = r.UnknownCallFile, r.UnknownCallLine
			return out
		}
	}
	for _, r := range rows {
		if r.Depth >= depth {
			out.Status, out.Stop = "partial", "depth"
			break
		}
	}
	return out
}
