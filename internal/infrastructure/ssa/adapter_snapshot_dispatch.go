package ssa

// 函数全集快照 + 派发注册收集（从 adapter_index.go 拆出——行数治理）：
// Q249 共享快照 + Q253 阶段进度（心跳/真实分母）。

import (
	"context"

	"github.com/schaepher/codeintel/internal/domain"
	"golang.org/x/tools/go/ssa"
)

// initFuncSnapshotDispatch 构建全程序函数快照并收集派发注册点：
//   - Q249：AllFunctions 一次，6 处消费者共享同一份 modFuncs；
//   - Q253：funcSnapshot 无真实分母 → 后台心跳持续刷新已用时间；
//   - Q221：dispatchRegs 必须在 Index 级初始化一次（sp.Build 之后——
//     MakeInterface 指令已构建）——零值 nil map 经 extractor 解引用
//     复制后 `ext.dispatchRegs == nil` 永远成立，cf_call.go 懒初始化
//     兜底导致每个函数都全程序 AllFunctions 扫描（go2o 12875 函数 ×
//     全图遍历 ≈ 305s CPU，pprof 46% 热点）。初始化后各 extractor 共享
//     只读 map；
//   - Q253：dispatchRegs 分母 = 模块函数总数（逐函数回报真实 %）。
func (a *Adapter) initFuncSnapshotDispatch(ctx context.Context, prog *ssa.Program, repo *domain.Repository, stages *stageReporter) []*ssa.Function {
	beat := stages.beginHeartbeat("funcSnapshot")
	a.funcs = newFuncSnapshotForModules(prog, repo.Modules)
	modFuncs := a.funcs.moduleFuncs(repo.Modules)
	beat()

	stages.begin("dispatchRegs", len(modFuncs))
	a.dispatchPkgs = nil
	a.dispatchRegs, a.dispatchPkgs = collectDispatchRegistrations(ctx, prog, modFuncs, repo.Modules, a.funcs, func(done int) {
		stages.rep.Advance("dispatchRegs", done)
	})
	a.regHits = buildRegHits(a.dispatchRegs, a.funcs)
	// Q252e：此标记把「建图」与「函数全集/派发注册」分开——日志停在
	// ssautil.Packages 时无法区分卡在串行建图还是 AllFunctions（必须 sample）。
	stages.stage("dispatchRegs")
	return modFuncs
}
