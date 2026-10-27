package orchestrator

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/ast"
	"github.com/schaepher/codeintel/internal/infrastructure/git"
	"github.com/schaepher/codeintel/internal/infrastructure/scip"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
	"github.com/schaepher/codeintel/internal/infrastructure/ssa"
	"go.uber.org/zap"
)

// New 创建 Orchestrator，默认挂载 MVP 适配器（SCIP/AST/Git）。
func New(repo *domain.Repository, db *sqlite.DB) *Orchestrator {
	logger := zap.L()
	logger.Debug("enter New")
	defer logger.Debug("exit New")
	return &Orchestrator{
		Repo:     repo,
		RepoImpl: sqlite.NewRepo(db),
		Adapters: []domain.IndexerPort{
			&scip.Adapter{},
			&ast.Adapter{},
			&git.Adapter{},
			&ssa.Adapter{},
		},
	}
}

// SetWorkers 设置 ssa 适配器按包并发数（Q170：CLI --workers 注入）。
func (o *Orchestrator) SetWorkers(n int) {
	for _, a := range o.Adapters {
		if w, ok := a.(interface{ SetWorkers(int) }); ok {
			w.SetWorkers(n)
		}
	}
}

// finishBuild 汇总构建状态并写 build_metadata（TD.md 9.2 降级矩阵）。
func (o *Orchestrator) finishBuild(start time.Time, results []AdapterResult, skipped int, toolName string) (*BuildResult, error) {

	status := domain.BuildSuccess
	errorMsgs := ""
	for _, r := range results {
		if r.Err == nil {
			continue
		}
		if r.Name == "scip" {
			status = domain.BuildFailed
		} else if status != domain.BuildFailed {
			status = domain.BuildDegraded
		}
		if errorMsgs != "" {
			errorMsgs += "; "
		}
		errorMsgs += fmt.Sprintf("%s: %v", r.Name, r.Err)
	}

	nodes, edges, _ := o.RepoImpl.Counts()
	duration := time.Since(start)

	// R6：降级统计（SQL 解析 AST 成功/失败/启发式）——"一直降级"
	// 提前暴露（R4 教训：AST 死代码静默半年）；todo12-13：包/类型检查
	// 覆盖与 SQL 统计分开（同一 JSON 对象不同键）
	cov := ssa.CoverageStats()
	statsMap := map[string]any{}
	if b, err := json.Marshal(ssa.SQLStats()); err == nil {
		_ = json.Unmarshal(b, &statsMap)
	}
	statsMap["pkg_total"] = cov.Packages
	statsMap["pkg_with_errors"] = cov.PackagesWithErrs
	statsMap["typecheck_errors"] = cov.TypeErrors
	if len(cov.ErrorPackages) > 0 {
		statsMap["error_packages"] = cov.ErrorPackages
	}
	statsJSON, _ := json.Marshal(statsMap)
	// 类型检查失败 = 覆盖不完整：success 不算完整（scip 失败仍 failed）
	if cov.TypeErrors > 0 || cov.PackagesWithErrs > 0 {
		if status != domain.BuildFailed {
			status = domain.BuildDegraded
		}
		msg := fmt.Sprintf("ssa: %d package(s) with errors (%d type errors)",
			cov.PackagesWithErrs, cov.TypeErrors)
		if errorMsgs != "" {
			errorMsgs += "; "
		}
		errorMsgs += msg
	}

	build := &BuildResult{
		Status:       status,
		Nodes:        nodes,
		Edges:        edges,
		Duration:     duration,
		CommitSHA:    headCommitSHA(o.Repo.Path),
		Adapter:      results,
		SkippedEdges: skipped,
		DegradeStats: string(statsJSON),
	}

	// P0-2：dispatch 相关包汇总（注册点包 ∪ 动态调用包——增量构建补
	// Load 用；含增量构建自身（补 Load 的包重新扫描后仍会收集）
	var dispatchPkgs []string
	seenPkg := map[string]bool{}
	for _, r := range results {
		for _, p := range r.DispatchPkgs {
			if !seenPkg[p] {
				seenPkg[p] = true
				dispatchPkgs = append(dispatchPkgs, p)
			}
		}
	}
	// P0-2 根治：与上一轮记录 union（只能增、不能减）——增量构建仅 Load
	// 变更包 + 上轮 dispatch 包，本轮收集的是子集，直接覆盖会让集合逐轮
	// 缩小（改 impl 包每次丢 dispatch_to 边）。已删除的包由
	// mergeDispatchPkgPatterns 的目录存在性检查跳过。
	if prev, err := o.RepoImpl.GetLatest(); err == nil && prev != nil {
		dispatchPkgs = mergeDispatchPkgs(dispatchPkgs, prev.DispatchPkgs)
	}

	buildID := newBuildID()
	meta := &domain.BuildMeta{
		BuildID:      buildID,
		CommitSHA:    build.CommitSHA,
		ToolName:     toolName,
		Status:       status,
		DurationMs:   duration.Milliseconds(),
		ErrorMsg:     errorMsgs,
		Nodes:        nodes,
		Edges:        edges,
		DegradeStats: string(statsJSON),
		DispatchPkgs: dispatchPkgs,

		WorktreeFingerprint: o.worktreeFP,
	}
	if err := o.RepoImpl.Save(meta); err != nil {
		return build, fmt.Errorf("save build metadata: %w", err)
	}
	// 暴露实际落库身份（与 build_metadata 同一值；HeadCommit 在 CommitSHA）
	build.BuildID = buildID
	return build, nil
}
func newBatch() *batchT {
	logger := zap.L()
	logger.Debug("enter newBatch")
	defer logger.Debug("exit newBatch")
	return &batchT{dedup: newNodeDedup()}
}

// batchPool 批次对象复用（todo9-7）：每批 4 个切片会增长到 BatchSize，
// 构建期多批重复分配；flush 完成后归还（切片保留容量 + 去重表 clear）。
var batchPool = sync.Pool{New: func() any { return newBatch() }}

// getBatch 取一个已清空的批次对象。
func getBatch() *batchT {
	b := batchPool.Get().(*batchT)
	b.reset()
	return b
}

// putBatch 归还批次对象（flush 之后调用；切片内容不再被任何方持有）。
func putBatch(b *batchT) {
	if b == nil {
		return
	}
	b.reset()
	batchPool.Put(b)
}

// mergeDispatchPkgs dispatch 相关包**只增不减**（P0-2 根治）：先去重 current
// （保持其顺序），再追加 prev 中未出现项。纯函数，便于单测。
func mergeDispatchPkgs(current, prev []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(current)+len(prev))
	for _, p := range current {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, p := range prev {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// flush 将当前批次写入数据库。
func (o *Orchestrator) flush(b *batchT, mu *sync.Mutex, skipped *int) error {
	logger := zap.L()
	logger.Debug("enter (Orchestrator).flush")
	defer logger.Debug("exit (Orchestrator).flush")
	if len(b.nodes) == 0 && len(b.edges) == 0 && len(b.summaries) == 0 {
		return nil
	}

	flushStart := time.Now()
	res, err := o.RepoImpl.SaveBatchStats(b.nodes, b.edges, b.summaries, b.origins)
	if time.Since(flushStart) > 100*time.Millisecond {
		logger.Info("flush slow", zap.Duration("elapsed", time.Since(flushStart)),
			zap.Int("nodes", len(b.nodes)), zap.Int("edges", len(b.edges)),
			zap.Int("summaries", len(b.summaries)), zap.Int("origins", len(b.origins)))
	}
	if err != nil {
		return err
	}
	mu.Lock()
	mu.Unlock()

	o.failedEdges = append(o.failedEdges, res.FailedEdges...)
	o.failedSummaries = append(o.failedSummaries, res.FailedSummaries...)
	o.failedOrigins = append(o.failedOrigins, res.FailedOrigins...)
	b.nodes = b.nodes[:0]
	b.edges = b.edges[:0]
	b.summaries = b.summaries[:0]
	return nil
}

// retryFailedFK 构建尾部重试 FK 失败项（P2）：全部节点落库后，跨批
// 依赖已满足 → 绝大多数重试成功；仍失败的为真缺节点（如 Git 追踪到
// 未索引文件），计入跳过数（SkippedEdges 语义：最终跳过）。
func (o *Orchestrator) retryFailedFK(skipped *int) {
	logger := zap.L()
	if len(o.failedEdges) == 0 && len(o.failedSummaries) == 0 && len(o.failedOrigins) == 0 {
		return
	}
	logger.Info("retry FK-failed items",
		zap.Int("edges", len(o.failedEdges)),
		zap.Int("summaries", len(o.failedSummaries)),
		zap.Int("origins", len(o.failedOrigins)))
	res, err := o.RepoImpl.SaveBatchStats(nil, o.failedEdges, o.failedSummaries, o.failedOrigins)
	o.failedEdges = nil
	o.failedSummaries = nil
	o.failedOrigins = nil
	if err != nil {
		logger.Warn("retry FK-failed items", zap.Error(err))
		return
	}

	// 重试后**仍**解析不到端点的边 = 真悬挂边（永久跳过）——按 len 计。
	*skipped += len(res.FailedEdges)
	if len(res.FailedEdges) > 0 {
		logger.Info("drop unresolvable edges", zap.Int("edges", len(res.FailedEdges)))
	}
}

// GetRepo 返回仓储（查询命令共用）。
func (o *Orchestrator) GetRepo() *sqlite.Repo {
	logger := zap.L()
	logger.Debug("enter (Orchestrator).GetRepo")
	defer logger.Debug("exit (Orchestrator).GetRepo")
	return o.RepoImpl
}
