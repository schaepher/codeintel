package sqlite

// todo23 14.4-B：子进程内**查询生命周期内**的临时文件峰值采样。
//
// 父进程的 `/proc/<pid>/fd` 轮询在短查询（query_ms < 采样间隔）时可能一个
// 查询期采样点都没有；这里在子进程内部、查询边界内同步采样：
//   - 查询开始/结束各做一次快照（短查询至少有这两个覆盖点）；
//   - 查询期间 250µs 轮询（含已 unlink 仍打开的 fd）；
//   - 记录创建/扩容/消失事件（同 fd 目标大小变化 + 新/消失目标）。
//
// 只用于**测量**，不改生产查询；平台无 /proc 时返回 0 样本并交给父进程
// 回退（父进程仍按 unavailable 处理）。

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// benchTempEvent 一个临时文件事件（create / grow / unlink）。
type benchTempEvent struct {
	TimeMS float64 `json:"t_ms"`
	Target string  `json:"target"`
	Size   int64   `json:"size"`
	State  string  `json:"state"` // create|grow|unlink
}

// selfTempSampler 子进程内采样器（tmpDir 下的 fd，含 deleted 目标）。
type selfTempSampler struct {
	tmpDir   string
	interval time.Duration

	mu          sync.Mutex
	peak        int64
	samples     int
	maxGap      time.Duration
	last        time.Time
	start       time.Time
	prev        map[string]int64
	prevDeleted map[string]bool
	events      []benchTempEvent
	note        string
}

func newSelfTempSampler(tmpDir string) *selfTempSampler {
	return &selfTempSampler{tmpDir: tmpDir, interval: 250 * time.Microsecond,
		prev: map[string]int64{}, prevDeleted: map[string]bool{}, start: time.Now()}
}

// snapshot 采样一次；diff 生成事件。phase 非空时在事件里标注边界。
func (s *selfTempSampler) snapshot(phase string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if !s.last.IsZero() {
		if gap := now.Sub(s.last); gap > s.maxGap {
			s.maxGap = gap
		}
	}
	s.last = now
	fdDir := "/proc/self/fd"
	entries, err := os.ReadDir(fdDir)
	if err != nil {
		s.note = fmt.Sprintf("read %s: %v", fdDir, err)
		return
	}
	cur := map[string]int64{}
	curDeleted := map[string]bool{}
	var total int64
	for _, e := range entries {
		fdPath := filepath.Join(fdDir, e.Name())
		target, err := os.Readlink(fdPath)
		if err != nil || !strings.HasPrefix(target, s.tmpDir) {
			continue
		}
		fi, err := os.Stat(fdPath)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		deleted := strings.HasSuffix(target, " (deleted)")
		base := strings.TrimSuffix(target, " (deleted)")
		cur[base] = fi.Size()
		curDeleted[base] = deleted
		total += fi.Size()
	}
	ts := float64(now.Sub(s.start).Microseconds()) / 1000
	for target, size := range cur {
		old, ok := s.prev[target]
		switch {
		case !ok:
			s.events = append(s.events, benchTempEvent{TimeMS: ts, Target: target, Size: size, State: "create"})
			if curDeleted[target] {
				s.events = append(s.events, benchTempEvent{TimeMS: ts, Target: target, Size: size, State: "unlink"})
			}
		default:
			if size > old {
				s.events = append(s.events, benchTempEvent{TimeMS: ts, Target: target, Size: size, State: "grow"})
			}
			if curDeleted[target] && !s.prevDeleted[target] {
				s.events = append(s.events, benchTempEvent{TimeMS: ts, Target: target, Size: size, State: "unlink"})
			}
		}
	}
	for target, old := range s.prev {
		if _, ok := cur[target]; ok {
			continue
		}
		s.events = append(s.events, benchTempEvent{TimeMS: ts, Target: target, Size: old, State: "close"})
	}
	s.prev = cur
	s.prevDeleted = curDeleted
	s.samples++
	if total > s.peak {
		s.peak = total
	}
	s.note = ""
}

// loop 固定间隔采样直到 stop。
func (s *selfTempSampler) loop(stop <-chan struct{}) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			s.snapshot("")
		}
	}
}

// benchFixtureTempChoreography 可控夹具：创建 4KB → 扩到 64KB → unlink 后
// 继续持有 fd（查询期间一直可观测）→ 查询结束后关闭。返回关闭函数。
//
// 说明：SQLite 短查询本身不保证产临时文件；该夹具用于验证"短查询期间
// 临时文件峰值采集"的机制（含 unlink 仍打开），断言精确最大字节数。
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
