package action

// 批次 C 迁移：`query unused` 编排（原 cli/unused.go 的 git diff +
// 未调用分析）——--since 时执行 git diff 解析为 SinceInfo 再分析；
// cli 只留参数解析与输出（表格/JSON/--fail-on 退出码）。

import (
	"fmt"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// UnusedRequest query unused 参数（flag 解析在 cli）。
type UnusedRequest struct {
	RepoAbs string // --since 时 git diff 的工作目录
	Since   string // git ref（空 = 全量报告）
}

// UnusedQuery 未调用函数与孤立链分析（field_trace.md §16）：
//   - 无 --since：全量报告（冗余代码检查）
//   - --since <ref>：git diff 区间内新增/修改函数（流程衔接检查）
func (a *Actions) UnusedQuery(req UnusedRequest) (*UnusedReport, error) {
	logger := zap.L()
	logger.Info("enter (Actions).UnusedQuery", zap.String("repo", req.RepoAbs), zap.String("since", req.Since))
	defer logger.Info("exit (Actions).UnusedQuery")
	var since *domain.SinceInfo
	if req.Since != "" {
		s, err := RunGitDiffSince(req.RepoAbs, req.Since)
		if err != nil {
			return nil, fmt.Errorf("git diff %s: %v", req.Since, err)
		}
		since = s
	}
	return a.Unused(since)
}
