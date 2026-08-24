package cli

import "context"
import "errors"
import "github.com/modelcontextprotocol/go-sdk/mcp"
import "github.com/schaepher/codeintel/internal/action"
import "github.com/schaepher/codeintel/internal/domain"
import "github.com/schaepher/codeintel/internal/infrastructure/sqlite"

// registerAdminTools 注册工具子集（Q252：registerMCPTools 按组拆分——行数治理 ≤300）。
func registerAdminTools(server *mcp.Server, env *mcpEnv, r *sqlite.Repo, repoAbs string) {

	mcp.AddTool(server, &mcp.Tool{Name: "batch_symbols", Description: "批量符号概览（多符号一次返回；单输入失败跳过，部分成功）"},
		mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args batchParams) (*mcp.CallToolResult, BatchOut, error) {
			res, err := a.BatchSymbols(args.Symbols)
			if err != nil {
				return toolErr(err.Error()), BatchOut{}, nil
			}
			out := BatchOut{Results: res}
			return toolJSON(out), out, nil
		}))

	mcp.AddTool(server, &mcp.Tool{Name: "update", Description: "增量更新索引（git 检测变更的 .go 文件重建；索引 stale 时调用自愈）"},
		mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args updateParams) (*mcp.CallToolResult, buildResult, error) {
			res2, out := runBuildTool(ctx, repoAbs, false)
			return res2, out, nil
		}))

	mcp.AddTool(server, &mcp.Tool{Name: "init", Description: "全量重建索引（schema/分析逻辑变更后；大仓库耗时较长）"},
		mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args updateParams) (*mcp.CallToolResult, buildResult, error) {
			res2, out := runBuildTool(ctx, repoAbs, true)
			return res2, out, nil
		}))

	mcp.AddTool(server, &mcp.Tool{Name: "roots", Description: "顶层入口（main + 服务入口）——陌生仓库先看入口再深入"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args updateParams) (*mcp.CallToolResult, RootsOut, error) {
			roots, err := a.Roots()
			if err != nil {
				return toolErr(err.Error()), RootsOut{}, nil
			}
			out := RootsOut{Roots: nodeBriefList(roots)}
			return toolJSON(out), out, nil
		})))

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
				out.Build = latest
			}
			return toolJSON(out), out, nil
		})))

	mcp.AddTool(server, &mcp.Tool{Name: "file_symbols", Description: "file:line 定位符号（报错栈/日志行 → 候选符号列表）"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args fileSymbolsParams) (*mcp.CallToolResult, FileSymbolsOut, error) {
			syms, err := a.SymbolsAt(args.File, args.Line)
			if err != nil {
				return toolErr(err.Error()), FileSymbolsOut{}, nil
			}
			out := FileSymbolsOut{Symbols: nodeBriefList(syms)}
			return toolJSON(out), out, nil
		})))

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
