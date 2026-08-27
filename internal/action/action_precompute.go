package action

// R100 待办13：迁移收尾——precompute relations 编排（原 cli 直连
// sqlite：RelationProgress → PrecomputeAllRelations → 摘要）迁 action；
// cli 只留参数解析 + 进度回调（UI 行为）与结果渲染。

import (
	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// PrecomputeResult 全量 relations 预计算结果（cli 渲染用）。
type PrecomputeResult struct {
	Status string                   // already-done（本次未算）| done（本次完成）| running（抢占失败）
	Total  int                      // 表总数（进度摘要）
	Done   int                      // running 时当前进度
	Rels   []*domain.TableRelation  // done 时全量关联（摘要统计）
}

// PrecomputeRelations 全量 relations 预计算编排（Q228）：
// 1. 已完成（progress done）→ already-done（不重复计算）
// 2. 未完成 → PrecomputeAllRelations（progressFn 由 cli 提供——进度
//    打印是 UI 行为）
// 3. 完成后再查进度：done → 摘要数据；非 done（抢占失败）→ running
func (a *Actions) PrecomputeRelations(progressFn func(done, total int)) (*PrecomputeResult, error) {
	logger := zap.L()
	logger.Info("enter (Actions).PrecomputeRelations")
	defer logger.Info("exit (Actions).PrecomputeRelations")
	p, err := a.repo.RelationProgress()
	if err != nil {
		return nil, err
	}
	if p.Status == "done" {
		return &PrecomputeResult{Status: "already-done", Total: p.Total}, nil
	}
	if err := a.repo.PrecomputeAllRelations(progressFn); err != nil {
		return nil, err
	}
	p, err = a.repo.RelationProgress()
	if err != nil {
		return nil, err
	}
	if p.Status != "done" {
		// 抢占失败（已有任务在跑）
		return &PrecomputeResult{Status: "running", Total: p.Total, Done: p.Done}, nil
	}
	rels, err := a.repo.GetAllTableRelations("")
	if err != nil {
		return nil, err
	}
	return &PrecomputeResult{Status: "done", Total: p.Total, Rels: rels}, nil
}
