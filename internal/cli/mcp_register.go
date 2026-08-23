package cli

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// Q252：registerMCPTools 按工具组拆分（行数治理）——20 个工具
// 注册分三组，每组一个 helper（参数同 registerMCPTools）。

// registerQueryTools 注册工具子集（Q252：registerMCPTools 按组拆分——行数治理 ≤300）。
func registerQueryTools(server *mcp.Server, env *mcpEnv, r *sqlite.Repo, repoAbs string) {
	mcp.AddTool(server, &mcp.Tool{Name: "symbol", Description: "符号详情（调用者/被调用者/动态派发候选）"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args symbolParams) (*mcp.CallToolResult, SymbolOut, error) {
			d, err := a.SymbolDetail(args.ID)
			if err != nil {
				return toolErr(err.Error()), SymbolOut{}, nil
			}
			out := SymbolOut{
				ID:        string(d.Node.ID),
				Name:      d.Node.Name,
				Kind:      string(d.Node.Kind),
				File:      d.Node.FilePath,
				Line:      d.Node.LineStart,
				Signature: d.Node.Signature(),
				Doc:       d.Node.DocComment(),
				Callers:   factBriefs(d.Callers, "source"),
				Callees:   factBriefs(d.Callees, "target"),
			}
			return toolJSON(out), out, nil
		})))
	mcp.AddTool(server, &mcp.Tool{Name: "fields", Description: "函数字段读写摘要（direct_read/write + indirect_write）"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args fieldsParams) (*mcp.CallToolResult, FieldsOut, error) {
			n, rows, err := a.FunctionFields(args.Func)
			if err != nil {
				return toolErr(err.Error()), FieldsOut{}, nil
			}
			out := FieldsOut{Func: n.Name, Rows: rows}
			return toolJSON(out), out, nil
		})))
	mcp.AddTool(server, &mcp.Tool{Name: "callers", Description: "调用者（depth 默认 1）"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args graphParams) (*mcp.CallToolResult, GraphOut, error) {
			res2, out := graphTool(a, args, "callers")
			return res2, out, nil
		})))
	mcp.AddTool(server, &mcp.Tool{Name: "callees", Description: "被调用者（depth 默认 1）"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args graphParams) (*mcp.CallToolResult, GraphOut, error) {
			res2, out := graphTool(a, args, "callees")
			return res2, out, nil
		})))
	mcp.AddTool(server, &mcp.Tool{Name: "impact", Description: "影响分析（depth 默认 3）"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args graphParams) (*mcp.CallToolResult, ImpactOut, error) {
			n, err := a.ResolveSymbol(args.Symbol)
			if err != nil {
				return toolErr(err.Error()), ImpactOut{}, nil
			}
			depth := args.Depth
			if depth <= 0 {
				depth = 3
			}
			nodes, err := a.Impact(n.ID, depth)
			if err != nil {
				return toolErr(err.Error()), ImpactOut{}, nil
			}
			out := ImpactOut{Target: string(n.ID), Nodes: nodeBriefList(nodes)}
			return toolJSON(out), out, nil
		})))
	mcp.AddTool(server, &mcp.Tool{Name: "trace", Description: "字段追溯（dir=backward/forward，max_depth 默认 8）"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args traceParams) (*mcp.CallToolResult, TraceOut, error) {
			depth := args.MaxDepth
			if depth <= 0 {
				depth = 8
			}
			_, rows, err := a.Trace(action.TraceParams{
				Field: args.Field, Func: args.Func, Forward: args.Dir == "forward", MaxDepth: depth,
			})
			if err != nil {
				return toolErr(err.Error()), TraceOut{}, nil
			}
			out := TraceOut{Steps: rows}
			return toolJSON(out), out, nil
		})))
	mcp.AddTool(server, &mcp.Tool{Name: "value_trace", Description: "数据值全链（跨函数；node 为节点 ID）"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args valueTraceParams) (*mcp.CallToolResult, ValueTraceOut, error) {
			id, err := a.ResolveAnchor(args.Node)
			if err != nil {
				return toolErr(err.Error()), ValueTraceOut{}, nil
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
				return toolErr(err.Error()), ValueTraceOut{}, nil
			}
			out := ValueTraceOut{Flows: rows}
			return toolJSON(out), out, nil
		})))
	mcp.AddTool(server, &mcp.Tool{Name: "context", Description: "跨层聚合上下文（symbol+callers/callees+fields+chain+traces）"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args contextParams) (*mcp.CallToolResult, *action.CodeContext, error) {
			c, err := a.Context(args.Node)
			if err != nil {
				return toolErr(err.Error()), nil, nil
			}
			return toolJSON(c), c, nil
		})))
	// R5：枚举权威清单（源码提取，不依赖索引——Agent 直接获取，
	// 避免重复定义枚举值导致转换成本）。R6：默认只返回有类型枚举；
	// include_untyped=true 放开（无类型字符串常量多为展示标签）
	mcp.AddTool(server, &mcp.Tool{Name: "enums", Description: "枚举常量权威清单（源码提取：类型/名称/值/注释/位置；默认只含显式类型枚举，include_untyped 放开）——写代码引用枚举时先查此工具"},
		mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args enumsParams) (*mcp.CallToolResult, []enumEntry, error) {
			out := extractEnums(repoAbs, !args.IncludeUntyped)
			return toolJSON(out), out, nil
		}))
	// R9：实体协作图 + 设计诊断（Agent 了解对象协作与设计信号，
	// 避免在不了解结构时盲目加新类型/依赖方向）
	mcp.AddTool(server, &mcp.Tool{Name: "entities", Description: "实体协作图 + 设计诊断（类型实体 + 包门面 + 方法互调聚合边 + 高耦合/循环/上帝对象/游离函数占比）——新增类型或依赖前先查协作结构"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args entitiesParams) (*mcp.CallToolResult, *domain.EntityGraph, error) {
			g, err := a.Entities()
			if err != nil {
				return toolErr(err.Error()), nil, nil
			}
			return toolJSON(g), g, nil
		})))
}

// registerTableTools 注册工具子集（Q252：registerMCPTools 按组拆分——行数治理 ≤300）。
func registerTableTools(server *mcp.Server, env *mcpEnv, r *sqlite.Repo, repoAbs string) {
	mcp.AddTool(server, &mcp.Tool{Name: "table", Description: "表级数据流聚合（列 + 写入方/读取方）"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args tableParams) (*mcp.CallToolResult, TableOut, error) {
			cols, err := a.Table(args.Name)
			if err != nil {
				return toolErr(err.Error()), nil, nil
			}
			return toolJSON(cols), cols, nil
		})))
	mcp.AddTool(server, &mcp.Tool{Name: "relations", Description: "表间关联（type 过滤 query/write/read/fk）"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args relationsParams) (*mcp.CallToolResult, RelationsOut, error) {
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
			return toolJSON(rels), rels, nil
		})))
	mcp.AddTool(server, &mcp.Tool{Name: "table_path", Description: "表 A → 表 B 数据通路（跨 mapping 表）"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args tablePathParams) (*mcp.CallToolResult, *action.TablePathResult, error) {
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
			return toolJSON(res), res, nil
		})))
	mcp.AddTool(server, &mcp.Tool{Name: "summary", Description: "跨层生命周期摘要（entry/compute/write/consume 主链）"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args summaryParams) (*mcp.CallToolResult, SummaryOut, error) {
			id, err := a.ResolveAnchor(args.Node)
			if err != nil {
				return toolErr(err.Error()), nil, nil
			}
			steps, err := a.SummaryChain(id)
			if err != nil {
				return toolErr(err.Error()), nil, nil
			}
			return toolJSON(steps), steps, nil
		})))
	mcp.AddTool(server, &mcp.Tool{Name: "module_calls", Description: "模块间调用（gRPC/HTTP）"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args moduleCallsParams) (*mcp.CallToolResult, ModuleCallsOut, error) {
			calls, err := a.ModuleCalls(args.Module)
			if err != nil {
				return toolErr(err.Error()), ModuleCallsOut{}, nil
			}
			out := ModuleCallsOut{Calls: calls}
			return toolJSON(out), out, nil
		})))
	// #228 写操作工具（不包 staleWrap——写后索引即最新，无需标注）。
	// batch_symbols：批量概览（复用 action.BatchSymbols，契约同 CLI batch --json）。
}

// registerAdminTools 注册工具子集（Q252：registerMCPTools 按组拆分——行数治理 ≤300）。
func registerAdminTools(server *mcp.Server, env *mcpEnv, r *sqlite.Repo, repoAbs string) {
	// #228 写操作工具（不包 staleWrap——写后索引即最新，无需标注）。
	// batch_symbols：批量概览（复用 action.BatchSymbols，契约同 CLI batch --json）。
	mcp.AddTool(server, &mcp.Tool{Name: "batch_symbols", Description: "批量符号概览（多符号一次返回；单输入失败跳过，部分成功）"},
		mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args batchParams) (*mcp.CallToolResult, BatchOut, error) {
			res, err := a.BatchSymbols(args.Symbols)
			if err != nil {
				return toolErr(err.Error()), BatchOut{}, nil
			}
			out := BatchOut{Results: res}
			return toolJSON(out), out, nil
		}))
	// update：增量更新（git 检测变更文件；stale 时调用自愈）。
	mcp.AddTool(server, &mcp.Tool{Name: "update", Description: "增量更新索引（git 检测变更的 .go 文件重建；索引 stale 时调用自愈）"},
		mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args updateParams) (*mcp.CallToolResult, buildResult, error) {
			res2, out := runBuildTool(ctx, repoAbs, false)
			return res2, out, nil
		}))
	// init：全量重建（schema/分析逻辑变更后；大仓库耗时较长）。
	mcp.AddTool(server, &mcp.Tool{Name: "init", Description: "全量重建索引（schema/分析逻辑变更后；大仓库耗时较长）"},
		mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args updateParams) (*mcp.CallToolResult, buildResult, error) {
			res2, out := runBuildTool(ctx, repoAbs, true)
			return res2, out, nil
		}))
	// #229 概览与定位工具（读工具，包 staleWrap）。
	// roots：顶层入口（main + 服务入口）——Agent 面对陌生仓库先看入口。
	mcp.AddTool(server, &mcp.Tool{Name: "roots", Description: "顶层入口（main + 服务入口）——陌生仓库先看入口再深入"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args updateParams) (*mcp.CallToolResult, RootsOut, error) {
			roots, err := a.Roots()
			if err != nil {
				return toolErr(err.Error()), RootsOut{}, nil
			}
			out := RootsOut{Roots: nodeBriefList(roots)}
			return toolJSON(out), out, nil
		})))
	// repo_summary：仓库概览（规模 + 表数 + 最新构建）。
	mcp.AddTool(server, &mcp.Tool{Name: "repo_summary", Description: "仓库概览（节点/边/表规模 + 最新构建状态）"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args updateParams) (*mcp.CallToolResult, RepoSummaryOut, error) {
			nodes, edges, err := a.Counts()
			if err != nil {
				return toolErr(err.Error()), RepoSummaryOut{}, nil
			}
			tables, err := a.GetTables()
			if err != nil {
				return toolErr(err.Error()), RepoSummaryOut{}, nil
			}
			latest, err := a.Latest()
			if err != nil && !errors.Is(err, domain.ErrNotFound) {
				return toolErr(err.Error()), RepoSummaryOut{}, nil
			}
			out := RepoSummaryOut{
				Nodes:  nodes,
				Edges:  edges,
				Tables: len(tables),
			}
			if err == nil && latest != nil {
				out.Build = latest // domain.BuildMeta 自带 snake_case 契约
			}
			return toolJSON(out), out, nil
		})))
	// file_symbols：file:line → 符号（Agent 从编译报错/日志栈定位）。
	mcp.AddTool(server, &mcp.Tool{Name: "file_symbols", Description: "file:line 定位符号（报错栈/日志行 → 候选符号列表）"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args fileSymbolsParams) (*mcp.CallToolResult, FileSymbolsOut, error) {
			syms, err := a.SymbolsAt(args.File, args.Line)
			if err != nil {
				return toolErr(err.Error()), FileSymbolsOut{}, nil
			}
			out := FileSymbolsOut{Symbols: nodeBriefList(syms)}
			return toolJSON(out), out, nil
		})))
	// #237 recent_changes：最近变更（Agent 接手仓库先看动态）。
	mcp.AddTool(server, &mcp.Tool{Name: "recent_changes", Description: "最近变更（commit 按日期降序 + 变更文件 + 顶层符号；max_commits 默认 10）"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args recentChangesParams) (*mcp.CallToolResult, RecentChangesOut, error) {
			limit := 10
			if args.MaxCommits != nil && *args.MaxCommits > 0 {
				limit = *args.MaxCommits
			}
			commits, err := a.RecentChanges(limit)
			if err != nil {
				return toolErr(err.Error()), RecentChangesOut{}, nil
			}
			out := RecentChangesOut{Commits: commits}
			return toolJSON(out), out, nil
		})))
}
