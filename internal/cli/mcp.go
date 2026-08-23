package cli

// Q243 MCP：`codeintel mcp [--repo <path>]`——stdio MCP server
// （github.com/modelcontextprotocol/go-sdk，协议由 SDK 实现），把 query
// 能力暴露为 tools（Agent 直接调用；输出复用 --json 契约，
// docs/json-contract.md）。工具 = 参数结构体（json tag 即 inputSchema
// 字段名）+ handler（闭包捕获 Actions）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
	"github.com/schaepher/codeintel/internal/logging"
	"github.com/schaepher/codeintel/internal/orchestrator"
	"go.uber.org/zap"
)

// 工具参数结构体（json tag = inputSchema 字段名，snake_case）。
type symbolParams struct {
	Repo string `json:"repo,omitempty"` // #232 多仓库：空=默认仓库；否则短名/路径/module（Q238）
	ID   string `json:"id"`             // 符号名或 canonical ID
}
type fieldsParams struct {
	Repo string `json:"repo,omitempty"` // #232 多仓库：空=默认仓库；否则短名/路径/module（Q238）
	Func string `json:"func"`           // 函数名或 canonical ID
}
type graphParams struct {
	Repo   string `json:"repo,omitempty"` // #232 多仓库：空=默认仓库；否则短名/路径/module（Q238）
	Symbol string `json:"symbol"`         // 符号名或 canonical ID
	Depth  int    `json:"depth"`          // 深度（默认 1/3）
}
type traceParams struct {
	Repo     string `json:"repo,omitempty"` // #232 多仓库：空=默认仓库；否则短名/路径/module（Q238）
	Field    string `json:"field"`          // 类型限定字段路径（如 example.com/m.T.A）
	Func     string `json:"func"`           // 函数名
	Dir      string `json:"dir"`            // backward（产生点）或 forward（使用点）
	MaxDepth int    `json:"max_depth"`      // 深度（默认 8）
}
type valueTraceParams struct {
	Repo     string  `json:"repo,omitempty"` // #232 多仓库：空=默认仓库；否则短名/路径/module（Q238）
	Node     string  `json:"node"`           // 节点 ID（如 symbol:go:...:main#t.A.read@5）
	MaxDepth int     `json:"max_depth"`      // 深度（默认 8）
	MinConf  float64 `json:"min_conf"`       // 候选边置信度剪枝（默认 1.0）
}
type contextParams struct {
	Repo string `json:"repo,omitempty"` // #232 多仓库：空=默认仓库；否则短名/路径/module（Q238）
	Node string `json:"node"`           // 符号/字段路径
}
type tableParams struct {
	Repo string `json:"repo,omitempty"` // #232 多仓库：空=默认仓库；否则短名/路径/module（Q238）
	Name string `json:"name"`           // 表名
}
type relationsParams struct {
	Repo       string `json:"repo,omitempty"` // #232 多仓库：空=默认仓库；否则短名/路径/module（Q238）
	Table      string `json:"table"`          // 表名
	Type       string `json:"type"`           // 关联类型（逗号分隔，默认 query,write）
	MaxHops    int    `json:"max_hops"`       // 跳数上限
	MaxResults int    `json:"max_results"`    // 条数上限
}
type tablePathParams struct {
	Repo    string `json:"repo,omitempty"` // #232 多仓库：空=默认仓库；否则短名/路径/module（Q238）
	From    string `json:"from"`           // 起始表名
	To      string `json:"to"`             // 目标表名
	MaxHops int    `json:"max_hops"`       // 跳数上限（默认 6）
}
type summaryParams struct {
	Repo string `json:"repo,omitempty"` // #232 多仓库：空=默认仓库；否则短名/路径/module（Q238）
	Node string `json:"node"`           // 锚点（符号/字段路径）
}
type moduleCallsParams struct {
	Repo   string `json:"repo,omitempty"` // #232 多仓库：空=默认仓库；否则短名/路径/module（Q238）
	Module string `json:"module"`         // module 名过滤（可选）
}

// #228 写操作工具参数：batch_symbols 批量概览；update/init 重建索引
// （stale 自愈——Agent 一条消息从「过期」到「可用」）。
type batchParams struct {
	Repo    string   `json:"repo,omitempty"` // #232 多仓库：空=默认仓库；否则短名/路径/module（Q238）
	Symbols []string `json:"symbols"`        // 符号名列表（单输入失败跳过，部分成功）
}
type updateParams struct {
	Repo string `json:"repo,omitempty"` // #232 多仓库：空=默认仓库；否则短名/路径/module（Q238）
}

// #229 file:line 定位参数。
type fileSymbolsParams struct {
	Repo string `json:"repo,omitempty"` // #232 多仓库：空=默认仓库；否则短名/路径/module（Q238）
	File string `json:"file"`           // 文件路径（精确或相对/省略前缀）
	Line int    `json:"line"`           // 行号（1 起）
}

// buildResult 重建结果摘要（update/init 工具输出，snake_case 契约）。
type buildResult struct {
	Status       string `json:"status"`                  // success/degraded/failed/up_to_date/needs_full_build
	ChangedFiles int    `json:"changed_files,omitempty"` // 变更文件数（init 为 0）
	Nodes        int    `json:"nodes,omitempty"`
	Edges        int    `json:"edges,omitempty"`
	SkippedEdges int    `json:"skipped_edges,omitempty"`
	DurationMs   int64  `json:"duration_ms,omitempty"`
	CommitSHA    string `json:"commit_sha,omitempty"`
	Message      string `json:"message,omitempty"` // 提示信息（无变更/需全量重建等）
}

func (p symbolParams) getRepo() string      { return p.Repo }
func (p fieldsParams) getRepo() string      { return p.Repo }
func (p graphParams) getRepo() string       { return p.Repo }
func (p traceParams) getRepo() string       { return p.Repo }
func (p valueTraceParams) getRepo() string  { return p.Repo }
func (p contextParams) getRepo() string     { return p.Repo }
func (p tableParams) getRepo() string       { return p.Repo }
func (p relationsParams) getRepo() string   { return p.Repo }
func (p tablePathParams) getRepo() string   { return p.Repo }
func (p summaryParams) getRepo() string     { return p.Repo }
func (p moduleCallsParams) getRepo() string { return p.Repo }
func (p batchParams) getRepo() string       { return p.Repo }
func (p fileSymbolsParams) getRepo() string { return p.Repo }
func (p updateParams) getRepo() string      { return p.Repo }

// toolErr 错误结果（isError=true，文本为错误信息）。
func toolErr(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
		IsError: true,
	}
}

// #232 多仓库：mcpEnv 持有默认仓库 + 已解析仓库缓存（ref → 条目）。
type mcpEnv struct {
	defaultActs *action.Actions
	cache       map[string]mcpRepoEntry
	mu          sync.Mutex
}

// mcpRepoEntry 已解析仓库的 acts + 仓库路径 + repo（stale 标注用）。
type mcpRepoEntry struct {
	acts *action.Actions
	abs  string
	repo *sqlite.Repo
}

// mcpRepoArg 参数结构体须提供仓库引用（getRepo 由脚本生成）。
type mcpRepoArg interface {
	getRepo() string
}

// mcpRepo 包装工具 handler：args.Repo 非空时解析目标仓库（#232——
// Q238 短名/路径后缀/module）并切到对应 Actions；跨仓库结果追加目标
// 仓库 stale 标注（默认仓库的 stale 标注由外层 staleWrap 负责）。
func mcpRepo[In mcpRepoArg, Out any](env *mcpEnv, inner func(*action.Actions, context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error)) func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, args In) (*mcp.CallToolResult, Out, error) {
		a := env.defaultActs
		if ref := args.getRepo(); ref != "" {
			entry, err := mcpResolveRepo(env, ref)
			if err != nil {
				var z Out
				return toolErr(err.Error()), z, nil
			}
			a = entry.acts
			res, out, err := inner(a, ctx, req, args)
			if err == nil && res != nil && !res.IsError {
				if tip := staleInfo(entry.abs, entry.repo); tip != "" {
					res.Content = append(res.Content, &mcp.TextContent{Text: "[stale] " + tip})
				}
			}
			return res, out, err
		}
		return inner(a, ctx, req, args)
	}
}

// mcpResolveRepo 解析仓库引用并打开对应库（结果缓存，并发加锁）。
func mcpResolveRepo(env *mcpEnv, ref string) (mcpRepoEntry, error) {
	env.mu.Lock()
	defer env.mu.Unlock()
	if e, ok := env.cache[ref]; ok {
		return e, nil
	}
	abs, _, err := resolveRepo(ResolveRepoRef(ref))
	if err != nil {
		return mcpRepoEntry{}, err
	}
	db, err := sqlite.Open(abs)
	if err != nil {
		return mcpRepoEntry{}, err
	}
	repo := sqlite.NewRepo(db)
	e := mcpRepoEntry{acts: action.New(repo), abs: abs, repo: repo}
	env.cache[ref] = e
	return e, nil
}

// staleWrap 包装工具 handler（Q243 新鲜度）：结果非错误且索引过期时，
// content 追加 [stale] 标注——Agent 可见；content[0] 契约 JSON 不变。
func staleWrap[In, Out any](r *sqlite.Repo, repoAbs string, inner func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error)) func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, args In) (*mcp.CallToolResult, Out, error) {
		res, out, err := inner(ctx, req, args)
		if err == nil && res != nil && !res.IsError {
			if tip := staleInfo(repoAbs, r); tip != "" {
				res.Content = append(res.Content, &mcp.TextContent{Text: "[stale] " + tip})
			}
		}
		return res, out, err
	}
}

// toolJSON 成功结果：契约 JSON 文本（docs/json-contract.md）。
func toolJSON(v any) *mcp.CallToolResult {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return toolErr("marshal result: " + err.Error())
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}
}

// registerMCPTools 注册全部工具（handler 闭包捕获 env；staleWrap 追加
// [stale] 标注——Q243 新鲜度显式化；mcpRepo 支持 #232 多仓库）。
func registerMCPTools(server *mcp.Server, env *mcpEnv, r *sqlite.Repo, repoAbs string) {
	mcp.AddTool(server, &mcp.Tool{Name: "symbol", Description: "符号详情（调用者/被调用者/动态派发候选）"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args symbolParams) (*mcp.CallToolResult, any, error) {
			d, err := a.SymbolDetail(args.ID)
			if err != nil {
				return toolErr(err.Error()), nil, nil
			}
			out := map[string]any{
				"id":        string(d.Node.ID),
				"name":      d.Node.Name,
				"kind":      string(d.Node.Kind),
				"file":      d.Node.FilePath,
				"line":      d.Node.LineStart,
				"signature": d.Node.Signature(),
				"doc":       d.Node.DocComment(),
				"callers":   factIDs(d.Callers, "source"),
				"callees":   factIDs(d.Callees, "target"),
			}
			return toolJSON(out), nil, nil
		})))
	mcp.AddTool(server, &mcp.Tool{Name: "fields", Description: "函数字段读写摘要（direct_read/write + indirect_write）"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args fieldsParams) (*mcp.CallToolResult, any, error) {
			n, rows, err := a.FunctionFields(args.Func)
			if err != nil {
				return toolErr(err.Error()), nil, nil
			}
			return toolJSON(map[string]any{"func": n.Name, "rows": rows}), nil, nil
		})))
	mcp.AddTool(server, &mcp.Tool{Name: "callers", Description: "调用者（depth 默认 1）"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args graphParams) (*mcp.CallToolResult, any, error) {
			return graphTool(a, args, "callers"), nil, nil
		})))
	mcp.AddTool(server, &mcp.Tool{Name: "callees", Description: "被调用者（depth 默认 1）"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args graphParams) (*mcp.CallToolResult, any, error) {
			return graphTool(a, args, "callees"), nil, nil
		})))
	mcp.AddTool(server, &mcp.Tool{Name: "impact", Description: "影响分析（depth 默认 3）"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args graphParams) (*mcp.CallToolResult, any, error) {
			n, err := a.ResolveSymbol(args.Symbol)
			if err != nil {
				return toolErr(err.Error()), nil, nil
			}
			depth := args.Depth
			if depth <= 0 {
				depth = 3
			}
			nodes, err := a.Impact(n.ID, depth)
			if err != nil {
				return toolErr(err.Error()), nil, nil
			}
			return toolJSON(map[string]any{"target": string(n.ID), "nodes": nodeBriefs(nodes)}), nil, nil
		})))
	mcp.AddTool(server, &mcp.Tool{Name: "trace", Description: "字段追溯（dir=backward/forward，max_depth 默认 8）"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args traceParams) (*mcp.CallToolResult, any, error) {
			depth := args.MaxDepth
			if depth <= 0 {
				depth = 8
			}
			_, rows, err := a.Trace(action.TraceParams{
				Field: args.Field, Func: args.Func, Forward: args.Dir == "forward", MaxDepth: depth,
			})
			if err != nil {
				return toolErr(err.Error()), nil, nil
			}
			return toolJSON(map[string]any{"steps": rows}), nil, nil
		})))
	mcp.AddTool(server, &mcp.Tool{Name: "value_trace", Description: "数据值全链（跨函数；node 为节点 ID）"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args valueTraceParams) (*mcp.CallToolResult, any, error) {
			id, err := a.ResolveAnchor(args.Node)
			if err != nil {
				return toolErr(err.Error()), nil, nil
			}
			depth := args.MaxDepth
			if depth <= 0 {
				depth = 8
			}
			minConf := args.MinConf
			if minConf <= 0 {
				minConf = 1.0
			}
			rows, err := a.ValueTrace(id, depth, minConf, false)
			if err != nil {
				return toolErr(err.Error()), nil, nil
			}
			return toolJSON(map[string]any{"flows": rows}), nil, nil
		})))
	mcp.AddTool(server, &mcp.Tool{Name: "context", Description: "跨层聚合上下文（symbol+callers/callees+fields+chain+traces）"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args contextParams) (*mcp.CallToolResult, any, error) {
			c, err := a.Context(args.Node)
			if err != nil {
				return toolErr(err.Error()), nil, nil
			}
			return toolJSON(c), nil, nil
		})))
	mcp.AddTool(server, &mcp.Tool{Name: "table", Description: "表级数据流聚合（列 + 写入方/读取方）"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args tableParams) (*mcp.CallToolResult, any, error) {
			cols, err := a.Table(args.Name)
			if err != nil {
				return toolErr(err.Error()), nil, nil
			}
			return toolJSON(cols), nil, nil
		})))
	mcp.AddTool(server, &mcp.Tool{Name: "relations", Description: "表间关联（type 过滤 query/write/read/fk）"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args relationsParams) (*mcp.CallToolResult, any, error) {
			rels, err := a.Relations(args.Table, "")
			if err != nil {
				return toolErr(err.Error()), nil, nil
			}
			if args.Type != "" {
				rels = filterRelTypes(rels, args.Type)
			}
			if args.MaxHops > 0 {
				rels = filterRels(rels, func(r *domain.TableRelation) bool { return r.Hops <= args.MaxHops })
			}
			if args.MaxResults > 0 && len(rels) > args.MaxResults {
				rels = rels[:args.MaxResults]
			}
			return toolJSON(rels), nil, nil
		})))
	mcp.AddTool(server, &mcp.Tool{Name: "table_path", Description: "表 A → 表 B 数据通路（跨 mapping 表）"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args tablePathParams) (*mcp.CallToolResult, any, error) {
			from, _, err := a.ResolveTableName(args.From)
			if err != nil {
				return toolErr("起始表: " + err.Error()), nil, nil
			}
			to, _, err := a.ResolveTableName(args.To)
			if err != nil {
				return toolErr("目标表: " + err.Error()), nil, nil
			}
			maxHops := args.MaxHops
			if maxHops <= 0 {
				maxHops = 6
			}
			res, err := a.TablePath(from, to, maxHops)
			if err != nil {
				return toolErr(err.Error()), nil, nil
			}
			capTablePathCandidates(res) // Q244：候选默认截断（防爆炸）
			return toolJSON(res), nil, nil
		})))
	mcp.AddTool(server, &mcp.Tool{Name: "summary", Description: "跨层生命周期摘要（entry/compute/write/consume 主链）"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args summaryParams) (*mcp.CallToolResult, any, error) {
			id, err := a.ResolveAnchor(args.Node)
			if err != nil {
				return toolErr(err.Error()), nil, nil
			}
			steps, err := a.SummaryChain(id)
			if err != nil {
				return toolErr(err.Error()), nil, nil
			}
			return toolJSON(steps), nil, nil
		})))
	mcp.AddTool(server, &mcp.Tool{Name: "module_calls", Description: "模块间调用（gRPC/HTTP）"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args moduleCallsParams) (*mcp.CallToolResult, any, error) {
			calls, err := a.ModuleCalls(args.Module)
			if err != nil {
				return toolErr(err.Error()), nil, nil
			}
			return toolJSON(map[string]any{"calls": calls}), nil, nil
		})))
	// #228 写操作工具（不包 staleWrap——写后索引即最新，无需标注）。
	// batch_symbols：批量概览（复用 action.BatchSymbols，契约同 CLI batch --json）。
	mcp.AddTool(server, &mcp.Tool{Name: "batch_symbols", Description: "批量符号概览（多符号一次返回；单输入失败跳过，部分成功）"},
		mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args batchParams) (*mcp.CallToolResult, any, error) {
			res, err := a.BatchSymbols(args.Symbols)
			if err != nil {
				return toolErr(err.Error()), nil, nil
			}
			return toolJSON(map[string]any{"results": res}), nil, nil
		}))
	// update：增量更新（git 检测变更文件；stale 时调用自愈）。
	mcp.AddTool(server, &mcp.Tool{Name: "update", Description: "增量更新索引（git 检测变更的 .go 文件重建；索引 stale 时调用自愈）"},
		mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args updateParams) (*mcp.CallToolResult, any, error) {
			return runBuildTool(ctx, repoAbs, false), nil, nil
		}))
	// init：全量重建（schema/分析逻辑变更后；大仓库耗时较长）。
	mcp.AddTool(server, &mcp.Tool{Name: "init", Description: "全量重建索引（schema/分析逻辑变更后；大仓库耗时较长）"},
		mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args updateParams) (*mcp.CallToolResult, any, error) {
			return runBuildTool(ctx, repoAbs, true), nil, nil
		}))
	// #229 概览与定位工具（读工具，包 staleWrap）。
	// roots：顶层入口（main + 服务入口）——Agent 面对陌生仓库先看入口。
	mcp.AddTool(server, &mcp.Tool{Name: "roots", Description: "顶层入口（main + 服务入口）——陌生仓库先看入口再深入"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args updateParams) (*mcp.CallToolResult, any, error) {
			roots, err := a.Roots()
			if err != nil {
				return toolErr(err.Error()), nil, nil
			}
			return toolJSON(map[string]any{"roots": nodeBriefs(roots)}), nil, nil
		})))
	// repo_summary：仓库概览（规模 + 表数 + 最新构建）。
	mcp.AddTool(server, &mcp.Tool{Name: "repo_summary", Description: "仓库概览（节点/边/表规模 + 最新构建状态）"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args updateParams) (*mcp.CallToolResult, any, error) {
			nodes, edges, err := a.Counts()
			if err != nil {
				return toolErr(err.Error()), nil, nil
			}
			tables, err := a.GetTables()
			if err != nil {
				return toolErr(err.Error()), nil, nil
			}
			latest, err := a.Latest()
			if err != nil && !errors.Is(err, domain.ErrNotFound) {
				return toolErr(err.Error()), nil, nil
			}
			out := map[string]any{
				"nodes":  nodes,
				"edges":  edges,
				"tables": len(tables),
			}
			if err == nil && latest != nil {
				out["build"] = latest // domain.BuildMeta 自带 snake_case 契约
			}
			return toolJSON(out), nil, nil
		})))
	// file_symbols：file:line → 符号（Agent 从编译报错/日志栈定位）。
	mcp.AddTool(server, &mcp.Tool{Name: "file_symbols", Description: "file:line 定位符号（报错栈/日志行 → 候选符号列表）"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args fileSymbolsParams) (*mcp.CallToolResult, any, error) {
			syms, err := a.SymbolsAt(args.File, args.Line)
			if err != nil {
				return toolErr(err.Error()), nil, nil
			}
			return toolJSON(map[string]any{"symbols": nodeBriefs(syms)}), nil, nil
		})))
}

// runBuildTool 增量（full=false）/全量（full=true）重建，输出 JSON 摘要
// （#228 写操作工具；复用 CLI update/init 同款核心调用）。
func runBuildTool(ctx context.Context, abs string, full bool) *mcp.CallToolResult {
	logger := zap.L()
	logger.Debug("enter runBuildTool", zap.Bool("full", full))
	defer logger.Debug("exit runBuildTool")
	if full {
		repo, err := buildRepo(abs)
		if err != nil {
			return toolErr(err.Error())
		}
		db, err := sqlite.Open(abs)
		if err != nil {
			return toolErr(err.Error())
		}
		defer db.Close()
		orch := orchestrator.New(repo, db)
		result, err := orch.FullBuild(ctx)
		if err != nil {
			return toolErr(err.Error())
		}
		if result.CommitSHA != "" {
			refreshRepoAfterUpdate(abs, result.CommitSHA)
		}
		return toolJSON(buildSummary(result, 0))
	}
	changed, err := detectChangedGoFiles(abs)
	if err != nil {
		return toolErr(err.Error())
	}
	// module 级文件变更：影响模块范围，提示全量重建
	for _, f := range changed {
		if f == "go.mod" || f == "go.work" {
			return toolJSON(buildResult{Status: "needs_full_build", Message: "go.mod/go.work 已变更，影响模块范围，请用 init 全量重建"})
		}
	}
	if len(changed) == 0 {
		return toolJSON(buildResult{Status: "up_to_date", Message: "无变更的 .go 文件（索引已是最新）"})
	}
	repo, err := buildRepo(abs)
	if err != nil {
		return toolErr(err.Error())
	}
	db, err := sqlite.Open(abs)
	if err != nil {
		return toolErr(err.Error())
	}
	defer db.Close()
	orch := orchestrator.New(repo, db)
	result, err := orch.IncrementalBuild(ctx, changed)
	if err != nil {
		return toolErr(err.Error())
	}
	if result.CommitSHA != "" {
		refreshRepoAfterUpdate(abs, result.CommitSHA)
	}
	return toolJSON(buildSummary(result, len(changed)))
}

// buildSummary 构建结果转契约摘要。
func buildSummary(result *orchestrator.BuildResult, changed int) buildResult {
	return buildResult{
		Status:       string(result.Status),
		ChangedFiles: changed,
		Nodes:        result.Nodes,
		Edges:        result.Edges,
		SkippedEdges: result.SkippedEdges,
		DurationMs:   result.Duration.Milliseconds(),
		CommitSHA:    result.CommitSHA,
	}
}

// graphTool callers/callees 共用 handler。
func graphTool(acts *action.Actions, args graphParams, which string) *mcp.CallToolResult {
	n, err := acts.ResolveSymbol(args.Symbol)
	if err != nil {
		return toolErr(err.Error())
	}
	depth := args.Depth
	if depth <= 0 {
		depth = 1
	}
	var facts []*domain.Fact
	if which == "callers" {
		facts, err = acts.Callers(n.ID, depth)
	} else {
		facts, err = acts.Callees(n.ID, depth)
	}
	if err != nil {
		return toolErr(err.Error())
	}
	return toolJSON(map[string]any{"target": string(n.ID), "rows": facts})
}

// filterRelTypes 按类型过滤（空=不过滤；逗号分隔）。
func filterRelTypes(rels []*domain.TableRelation, types string) []*domain.TableRelation {
	want := map[string]bool{}
	for _, t := range strings.Split(types, ",") {
		want[strings.TrimSpace(t)] = true
	}
	return filterRels(rels, func(r *domain.TableRelation) bool { return want[r.Type] })
}

// filterRels 谓词过滤（保留满足的）。
func filterRels(rels []*domain.TableRelation, keep func(*domain.TableRelation) bool) []*domain.TableRelation {
	out := make([]*domain.TableRelation, 0, len(rels))
	for _, r := range rels {
		if keep(r) {
			out = append(out, r)
		}
	}
	return out
}

// mcpServer 组装 MCP server（工具注册；#232 多仓库 env）。
func mcpServer(acts *action.Actions, r *sqlite.Repo, repoAbs string) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "codeintel", Version: "0.1.0"}, nil)
	env := &mcpEnv{defaultActs: acts, cache: map[string]mcpRepoEntry{}}
	registerMCPTools(server, env, r, repoAbs)
	return server
}

// cmdMCP 实现 `codeintel mcp`（stdio MCP server）。
func cmdMCP(args []string) int {
	logger := zap.L()
	logger.Debug("enter cmdMCP")
	defer logger.Debug("exit cmdMCP")
	repoPath := "."
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--repo" && i+1 < len(args):
			repoPath = ResolveRepoRef(args[i+1])
			i++
		case strings.HasPrefix(a, "--repo="):
			repoPath = ResolveRepoRef(strings.TrimPrefix(a, "--repo="))
		case a == "--help" || a == "-h":
			fmt.Println("用法: codeintel mcp [--repo <path>]\n  stdio MCP server（tools/list + tools/call 暴露 query 能力）")
			return 0
		default:
			fmt.Fprintf(os.Stderr, "error: 未知参数 %q\n", a)
			return 2
		}
	}
	abs, _, err := resolveRepo(repoPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		if repoPath == "." {
			printRepoHint()
		}
		return 1
	}
	if err := logging.ToFile(abs); err != nil {
		fmt.Fprintf(os.Stderr, "warning: 日志切换失败: %v\n", err)
	}
	db, err := sqlite.Open(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()
	acts := action.New(sqlite.NewRepo(db))
	session, err := mcpServer(acts, sqlite.NewRepo(db), abs).Connect(context.Background(), &mcp.StdioTransport{}, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mcp: %v\n", err)
		return 1
	}
	session.Wait()
	return 0
}
