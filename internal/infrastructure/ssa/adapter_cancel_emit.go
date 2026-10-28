package ssa

// T17：取消感知的 emit 包装器（独立 helper，未接入生产发射路径）。
//
// 生产发射路径目前只在 acquireWorker 抢槽/回滚处检查取消；本辅助函数为
// 需要"发射前确认未取消"的调用方提供一个可测试的边界：已取消时不调用
// 底层写入并返回 false，正常 context 下调用一次并返回写入错误。
// 它不改变现有 Index/emitFunction 行为，也不宣称端到端取消接线。

import "context"

// emitUnlessCanceled 在 ctx 未取消时执行 emit；已取消直接返回 false。
// 返回值：true 表示已执行底层写入。
func emitUnlessCanceled(ctx context.Context, emit func() error) (bool, error) {
	if ctx.Err() != nil {
		return false, nil
	}
	if emit == nil {
		return true, nil
	}
	return true, emit()
}
