package sqlite

// todo23/todo24 14.4-B：临时文件峰值的**可控夹具**（hold / ephemeral）与
// 事件排序。夹具只负责在查询生命周期内制造可预期的文件变化，采集在
// repo_walk_bench_self_temp_test.go（快照/事件）与
// repo_walk_bench_watch_linux_test.go（inotify）里完成。

import (
	"fmt"
	"os"
	"sort"
	"testing"
	"time"
)

// eventsCopy 事件副本（监听协程可能仍在写，调用方拿到独立切片）。
func (s *selfTempSampler) eventsCopy() []benchTempEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]benchTempEvent(nil), s.events...)
}

func (s *selfTempSampler) appendEvent(ts float64, phase, target string, size int64, state string) {
	s.events = append(s.events, benchTempEvent{TimeMS: ts, Target: target, Size: size,
		State: state, Phase: phase})
}

// noteWatch 记录监听建立失败原因（平台/权限），不影响轮询回退。
func (s *selfTempSampler) noteWatch(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.watchErr = err.Error()
}

// fillTempStats 把采样器的最终状态写进子进程结果（含峰值依据/触发阶段）。
func (r *benchChildResult) fillTempStats(s *selfTempSampler) {
	peak, phase, samples, eventSamples, lostN, maxGap, note, watchErr, lost := s.stats()
	r.TempPeak = peak
	r.TempPeakPhase = phase
	r.TempSamples = samples
	r.TempEventSamples = eventSamples
	r.TempMaxGapMS = float64(maxGap.Microseconds()) / 1000
	r.TempEvents = sortedTempEvents(s.eventsCopy())
	r.TempWatchNote = watchErr
	r.TempLost = lost
	if samples == 0 {
		return
	}
	if watchErr != "" {
		r.TempPeakSource = "child_query_poll"
	} else {
		r.TempPeakSource = "child_query_events"
	}
	if lostN > 0 {
		// 有文件在两次采样之间被创建、扩容并 unlink（连事件快照都没赶上）：
		// 真实峰值不可知 → 只报下界，父进程按 unavailable 处理。
		r.TempPeakBasis = "lower_bound"
		r.TempPeakNote = fmt.Sprintf(
			"有 %d 个临时文件在两次采样之间创建/扩容并 unlink，峰值不可知；观测到的 %d 字节只是下界（%s）",
			lostN, peak, note)
		return
	}
	r.TempPeakBasis = "exact"
}

// waitSamplerMinPeak 等事件驱动采样记录到至少 min 字节（只在查询边界之外
// 调用，不拖长 query_ms）；事件机制不可用的平台直接返回，由边界快照兜底。
func waitSamplerMinPeak(t *testing.T, s *selfTempSampler, min int64, limit time.Duration) {
	t.Helper()
	if _, _, _, _, _, _, _, watchErr, _ := s.stats(); watchErr != "" {
		return
	}
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if peak, _, _, _, _, _, _, _, _ := s.stats(); peak >= min {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("事件驱动采样在 %s 内未记录到 %d 字节峰值（inotify 事件机制未生效）", limit, min)
}

// benchFixtureTempChoreography 可控夹具（hold 模式）：创建 4KB → 扩到 64KB
// → unlink 后继续持有 fd（查询期间一直可观测）→ 查询结束后关闭。
const (
	benchFixtureTempFinalSize = 64 * 1024
)

type benchTempFixture struct {
	file *os.File
	name string
}

// newBenchFixtureTemp 创建并扩展到最终大小（查询期间持有；unlink/close 由
// 调用方在查询边界后执行，用于记录 unlink 后继续持有与最后关闭事件）。
func newBenchFixtureTemp(dir string) *benchTempFixture {
	f, err := os.CreateTemp(dir, "bench-fixture-*")
	if err != nil {
		return nil
	}
	_ = f.Truncate(4096)
	return &benchTempFixture{file: f, name: f.Name()}
}

// Grow 扩展到最终大小（在查询边界内显式采样，产生 grow 事件）。
func (b *benchTempFixture) Grow() {
	if b != nil && b.file != nil {
		_ = b.file.Truncate(benchFixtureTempFinalSize)
	}
}

func (b *benchTempFixture) Unlink() {
	if b != nil && b.name != "" {
		_ = os.Remove(b.name)
	}
}

func (b *benchTempFixture) Close() {
	if b != nil && b.file != nil {
		_ = b.file.Close()
	}
}

// benchEphemeralTemp 可控夹具（ephemeral 模式）：在**没有任何轮询/边界
// 快照**参与的情况下创建 4KB → 扩到 64KB → 关闭，文件留在目录里，只能靠
// 事件驱动快照（或随后的目录枚举）观测到。用于验证"两次轮询之间创建、
// 扩容并关闭的短生命周期文件"不漏峰值。
type benchEphemeralTemp struct {
	name  string
	ready chan struct{}
	err   error
}

// newBenchEphemeralTemp 立即完成 create/grow/close（调用方不要 sleep 在查询
// 边界内；需要留时间给事件时在查询开始前等待 Ready）。
func newBenchEphemeralTemp(dir string) *benchEphemeralTemp {
	e := &benchEphemeralTemp{ready: make(chan struct{})}
	f, err := os.CreateTemp(dir, "bench-ephemeral-*")
	if err != nil {
		e.err = err
		close(e.ready)
		return e
	}
	e.name = f.Name()
	if err := f.Truncate(benchFixtureTempFinalSize); err != nil {
		e.err = err
	}
	if err := f.Close(); err != nil && e.err == nil {
		e.err = err
	}
	return e
}

// Remove 清理夹具文件（查询结束后调用）。
func (e *benchEphemeralTemp) Remove() {
	if e != nil && e.name != "" {
		_ = os.Remove(e.name)
	}
}

// sortedTempEvents 事件按时间/目标排序（输出稳定）。
func sortedTempEvents(in []benchTempEvent) []benchTempEvent {
	out := append([]benchTempEvent(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].TimeMS != out[j].TimeMS {
			return out[i].TimeMS < out[j].TimeMS
		}
		return out[i].Target < out[j].Target
	})
	return out
}
