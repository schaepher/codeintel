package ssa

import (
	"io"
	"sync"
	"testing"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/progress"
	"go.uber.org/zap"
)

// Q253：未注入时用逐行实现保底（格式与改动前一致：前缀 + heap 统计）。
func TestAdapterProgressFallbackIsPlain(t *testing.T) {
	a := &Adapter{}
	p, ok := a.prog().(*progress.Plain)
	if !ok {
		t.Fatalf("未注入进度时应为逐行实现，得到 %T", a.prog())
	}
	if p.Prefix != "[index] ssa 步骤" {
		t.Errorf("前缀不符：%q", p.Prefix)
	}
	if p.Detail == nil || p.Detail() == "" {
		t.Error("逐行模式应带 heap 统计（Q247/Q248 的内存排查靠这行）")
	}
}

// Q253：plain 模式下 SSA 换用自己的保底实现——保住 `[index] ssa 步骤`
// 前缀与 heap 统计（Q247/Q248 内存排查依赖它）。
func TestAdapterPlainInjectedKeepsSSAFormat(t *testing.T) {
	a := &Adapter{}
	a.SetProgress(progress.NewPlain(io.Discard, "[index] 步骤"))
	p, ok := a.prog().(*progress.Plain)
	if !ok {
		t.Fatalf("plain 注入后应仍为逐行实现，得到 %T", a.prog())
	}
	if p.Prefix != "[index] ssa 步骤" {
		t.Errorf("plain 模式必须用自己的前缀：%q", p.Prefix)
	}
	if p.Detail == nil {
		t.Error("plain 模式必须带 heap 统计")
	}
}

// Q253：注入优先，且 stage() 把耗时透传给实现（不自行记时钟）。
func TestAdapterProgressInjected(t *testing.T) {
	rec := &ssaRecProgress{}
	a := &Adapter{}
	a.SetProgress(rec)
	if a.prog() != domain.Progress(rec) {
		t.Fatal("注入的进度实现未被采用")
	}
	// 直接验证 stage 语义：Begin 后 End 带耗时与 nil error
	a.prog().Begin("x", 1, 0)
	a.prog().End("x", 5*time.Millisecond, nil)
	if len(rec.ends) != 1 || rec.ends[0] != "x（5ms）" {
		t.Fatalf("End 未携带耗时：%v", rec.ends)
	}
}

type ssaRecProgress struct {
	begins []string
	ends   []string
}

func (r *ssaRecProgress) Begin(name string, depth, total int) { r.begins = append(r.begins, name) }
func (r *ssaRecProgress) Advance(string, int)                 {}
func (r *ssaRecProgress) End(name string, elapsed time.Duration, err error) {
	r.ends = append(r.ends, name+"（"+elapsed.Round(time.Millisecond).String()+"）")
}
func (r *ssaRecProgress) Finish() {}

// hbProgress 心跳测试用进度实现（跨 goroutine，加锁）。
type hbProgress struct {
	mu           sync.Mutex
	advances     int
	advancesList []int
	ends         int
	endName      string
}

func (r *hbProgress) Begin(string, int, int) {}
func (r *hbProgress) Advance(_ string, done int) {
	r.mu.Lock()
	r.advances++
	r.advancesList = append(r.advancesList, done)
	r.mu.Unlock()
}
func (r *hbProgress) End(name string, _ time.Duration, _ error) {
	r.mu.Lock()
	r.ends++
	r.endName = name
	r.mu.Unlock()
}
func (r *hbProgress) Finish() {}

// TestUnknownDenominatorHeartbeat：未知分母阶段（funcSnapshot）后台心跳
// 持续 Advance 刷新耗时；end 后停止心跳并 End 一次。
func TestUnknownDenominatorHeartbeat(t *testing.T) {
	rec := &hbProgress{}
	s := &stageReporter{rep: rec, log: zap.NewNop(), start: time.Now(), heartbeat: time.Millisecond}
	end := s.beginHeartbeat("funcSnapshot")
	time.Sleep(25 * time.Millisecond)
	end()
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.advances == 0 {
		t.Error("心跳应周期性 Advance 刷新耗时")
	}
	if rec.ends != 1 || rec.endName != "funcSnapshot" {
		t.Errorf("end 应 End(funcSnapshot) 一次：ends=%d name=%q", rec.ends, rec.endName)
	}
}

// TestDispatchProgressPercent：dispatch 注册扫描按模块函数总数回报真实
// 百分比（分母 = len(modFuncs)）。
func TestDispatchProgressPercent(t *testing.T) {
	rec := &hbProgress{}
	s := &stageReporter{rep: rec, log: zap.NewNop(), start: time.Now(), heartbeat: time.Millisecond}
	s.begin("dispatchRegs", 3)
	for i := 1; i <= 3; i++ {
		s.rep.Advance("dispatchRegs", i)
	}
	s.stage("dispatchRegs")
	if len(rec.advancesList) != 3 || rec.advancesList[2] != 3 {
		t.Errorf("进度应为 1/2/3，得到 %v", rec.advancesList)
	}
}
