package domain

import "time"

// Progress 构建/长任务进度上报契约（Q253）。
//
// domain 只定义接口——渲染（TTY live bar / 逐行 / 静默）由
// internal/progress 与 cli 层实现；orchestrator 与各适配器只调这里的方法，
// 因此"换渲染"是换实现而不是加分支（--progress 的实现方式）。
//
// 耗时由调用方传入（各调用方本来就为日志算好了 `time.Since(start)`）——
// 实现不必自记时钟，测试可传固定值。
//
// 并发：并行适配器会从各自 goroutine 调 Begin/Advance/End，实现**必须**
// 自行加锁（接口不假设串行调用）。
type Progress interface {
	// Begin 声明一个步骤开始。depth 0=顶层、1=子步骤；total>0 表示有真实
	// 分母（可渲染百分比），<=0 表示只能显示"进行中 + 已用时"——**不要**
	// 编造假分母。
	Begin(name string, depth, total int)
	// Advance 上报某步骤已完成计数（按 name 定位；调用方保证 name 与
	// Begin 时一致）。
	Advance(name string, done int)
	// End 结束步骤：elapsed 为该步骤耗时，err != nil 表示失败（渲染 ✗，
	// 不吞错误——错误详情仍由调用方负责上报）。
	End(name string, elapsed time.Duration, err error)
	// Finish 收尾（清活动行 + 打印结束符）。
	Finish()
}

// NopProgress 静默实现（--progress none / 无终端场景）。
type NopProgress struct{}

func (NopProgress) Begin(string, int, int)           {}
func (NopProgress) Advance(string, int)              {}
func (NopProgress) End(string, time.Duration, error) {}
func (NopProgress) Finish()                          {}
