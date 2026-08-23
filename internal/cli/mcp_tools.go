package cli

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
	"github.com/schaepher/codeintel/internal/orchestrator"
	"go.uber.org/zap"
)

// runBuildTool 增量（full=false）/全量（full=true）重建，输出 JSON 摘要
// （#228 写操作工具；复用 CLI update/init 同款核心调用）。
func runBuildTool(ctx context.Context, abs string, full bool) (*mcp.CallToolResult, buildResult) {
	logger := zap.L()
	logger.Debug("enter runBuildTool", zap.Bool("full", full))
	defer logger.Debug("exit runBuildTool")
	if full {
		repo, err := buildRepo(abs)
		if err != nil {
			return toolErr(err.Error()), buildResult{}
		}
		db, err := sqlite.Open(abs)
		if err != nil {
			return toolErr(err.Error()), buildResult{}
		}
		defer db.Close()
		orch := orchestrator.New(repo, db)
		result, err := orch.FullBuild(ctx)
		if err != nil {
			return toolErr(err.Error()), buildResult{}
		}
		if result.CommitSHA != "" {
			refreshRepoAfterUpdate(abs, result.CommitSHA)
		}
		out := buildSummary(result, 0)
		return toolJSON(out), out
	}
	changed, err := detectChangedGoFiles(abs)
	if err != nil {
		return toolErr(err.Error()), buildResult{}
	}

	for _, f := range changed {
		if f == "go.mod" || f == "go.work" {
			out := buildResult{Status: "needs_full_build", Message: "go.mod/go.work 已变更，影响模块范围，请用 init 全量重建"}
			return toolJSON(out), out
		}
	}
	if len(changed) == 0 {
		out := buildResult{Status: "up_to_date", Message: "无变更的 .go 文件（索引已是最新）"}
		return toolJSON(out), out
	}
	repo, err := buildRepo(abs)
	if err != nil {
		return toolErr(err.Error()), buildResult{}
	}
	db, err := sqlite.Open(abs)
	if err != nil {
		return toolErr(err.Error()), buildResult{}
	}
	defer db.Close()
	orch := orchestrator.New(repo, db)
	result, err := orch.IncrementalBuild(ctx, changed)
	if err != nil {
		return toolErr(err.Error()), buildResult{}
	}
	if result.CommitSHA != "" {
		refreshRepoAfterUpdate(abs, result.CommitSHA)
	}
	out := buildSummary(result, len(changed))
	return toolJSON(out), out
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
		facts, err = acts.Callers(n.ID, depth)
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
