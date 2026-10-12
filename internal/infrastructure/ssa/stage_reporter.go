package ssa

import (
	"runtime"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// stageReporter SSA 阶段上报（Q253）：zap 日志 + 进度上报 + 峰值 heap
// profile 触发。stage(name) 报告**刚完成**的步骤耗时（语义与抽出前一致）；
// begin 在步骤开始前调用（TTY 模式据此显示"进行中"，total>0 时画进度条）。
type stageReporter struct {
	rep   domain.Progress
	log   *zap.Logger
	start time.Time
	// heartbeat 未知分母阶段的后台心跳间隔（测试可调小）。
	heartbeat time.Duration
}

func newStageReporter(rep domain.Progress, log *zap.Logger) *stageReporter {
	return &stageReporter{rep: rep, log: log, start: time.Now(), heartbeat: 500 * time.Millisecond}
}

// beginHeartbeat 未知分母阶段的开始：Begin(total=0) + 后台心跳周期性
// Advance（触发活动行重绘——TTY 阶段列表里的已用时间持续刷新，否则长
// 阶段会看起来卡死）。返回 end 函数（停心跳 + 走 stage 收尾，语义与
// 普通阶段一致）。
func (s *stageReporter) beginHeartbeat(name string) func() {
	s.rep.Begin(name, 1, 0)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		tk := time.NewTicker(s.heartbeat)
		defer tk.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tk.C:
				s.rep.Advance(name, 0)
			}
		}
	}()
	return func() {
		close(stop)
		<-done
		s.stage(name)
	}
}

func (s *stageReporter) begin(name string, total int) { s.rep.Begin(name, 1, total) }

func (s *stageReporter) stage(name string) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	elapsed := time.Since(s.start)
	s.log.Info("build stage",
		zap.String("stage", name), zap.Duration("elapsed", elapsed),
		zap.Int64("heap_mb", int64(ms.HeapAlloc>>20)),
		zap.Int64("heap_inuse_mb", int64(ms.HeapInuse>>20)))
	// Q247：HeapAlloc 含**未回收垃圾**（阶段边界未 GC）只当趋势看，
	// 真实 live 构成用 CODEINTEL_MEM_PROFILE 的 heap profile 归因。
	s.rep.End(name, elapsed, nil)
	dumpPeakHeapProfile(ms.HeapAlloc)
	s.start = time.Now()
}
