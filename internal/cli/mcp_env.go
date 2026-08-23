package cli

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

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
