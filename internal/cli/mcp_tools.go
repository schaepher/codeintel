package cli

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/orchestrator"
	"go.uber.org/zap"
)

// runBuildTool 增量（full=false）/全量（full=true）重建，输出 JSON 摘要
// （#228 写操作工具）。
// P1-03：构建流程统一走 action.Build——此前 MCP 自己拼一遍，缺 workers/
// 适配器超时/工作区指纹/VACUUM 判定，导致"MCP 构建后 stale 判定误报"
// 这类 CLI 与 MCP 行为分叉。
func runBuildTool(ctx context.Context, abs string, full bool) (*mcp.CallToolResult, buildResult) {
	logger := zap.L()
	logger.Debug("enter runBuildTool", zap.Bool("full", full))
	defer logger.Debug("exit runBuildTool")
	mode := action.BuildIncremental
	if full {
		mode = action.BuildFull
	}
	out, err := action.Build(ctx, action.BuildRequest{RepoPath: abs, Mode: mode})
	if err != nil {
		if out != nil && out.NeedsFull {
			res := buildResult{Status: "needs_full_build", Message: err.Error()}
			return toolJSON(res), res
		}
		return toolErr(err.Error()), buildResult{}
	}
	if out.NoChange {
		res := buildResult{Status: "up_to_date", Message: "无变更的 .go 文件（索引已是最新）"}
		return toolJSON(res), res
	}
	if out.Result == nil {
		return toolErr("构建未返回结果"), buildResult{}
	}
	res := buildSummary(out.Result, len(out.Changed))
	// 台账刷新口径与 CLI 一致：全量走 registerRepoAfterBuild（含 module/
	// worktree 归属），增量只刷新构建状态
	if out.Repo != nil {
		if full {
			registerRepoAfterBuild(abs, out.Repo.Module, len(out.Repo.Modules), out.Result.BuildID, out.Result.CommitSHA)
		} else {
			refreshRepoAfterUpdate(abs, out.Result.BuildID, out.Result.CommitSHA)
		}
	}
	return toolJSON(res), res
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
func graphTool(acts *action.Actions, args graphParams, which string) (*mcp.CallToolResult, GraphOut) {
	n, err := acts.ResolveSymbol(args.Symbol)
	if err != nil {
		return toolErr(err.Error()), GraphOut{}
	}
	depth := args.Depth
	if depth <= 0 {
		depth = 1
	}
	var facts []*domain.Fact
	if which == "callers" {
		facts, err = acts.CallersConcrete(n.ID, depth)
	} else {
		facts, err = acts.Callees(n.ID, depth)
	}
	if err != nil {
		return toolErr(err.Error()), GraphOut{}
	}
	out := GraphOut{Target: string(n.ID), Rows: facts}
	return toolJSON(out), out
}

// filterRelTypes 按类型过滤（空=不过滤；逗号分隔）。
func filterRelTypes(rels []*domain.TableRelation, types string) []*domain.TableRelation {
	want := map[string]bool{}
	for _, t := range strings.Split(types, ",") {
		want[strings.TrimSpace(t)] = true
	}
	return filterRels(rels, func(r *domain.TableRelation) bool { return want[string(r.Type)] })
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
