package sqlite

// todo23/todo24 14.4-B：子进程内**查询生命周期内**的临时文件峰值采集。
//
// 父进程的 `/proc/<pid>/fd` 轮询在短查询（query_ms < 采样间隔）时可能一个
// 查询期采样点都没有；这里在子进程内部、查询边界内采集：
//   - 查询开始/结束各做一次快照（短查询至少有这两个覆盖点）；
//   - 250µs 轮询（含已 unlink 仍打开的 fd）；
//   - **事件驱动**（todo24：inotify，见 repo_walk_bench_watch_linux_test.go）：
//     目录内任何 create/modify/close/delete 都立即触发一次快照，因此
//     "两次轮询之间创建、扩容并关闭"的短生命周期文件不会被漏掉；
//   - 快照除 /proc/self/fd 外还枚举 TMPDIR 目录项：文件在采样间隙被关闭但
//     仍留在目录里时，仍能按路径 stat 到大小并计入峰值；被关闭且已 unlink、
//     又从未被观测过的文件**不可知** → 记 `unobserved`，峰值标 `lower_bound`
//     （父进程据此输出 unavailable，绝不用 0 冒充观测）。
//
// 只用于**测量**，不改生产查询，也不 sleep/阻塞查询；平台无 /proc 时返回
// 0 样本并交给父进程回退（父进程仍按 unavailable 处理）。

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// benchTempPollEnv 采样轮询间隔覆写（毫秒；0 = 关闭轮询，只留边界快照与
// 事件驱动采样）。仅供夹具验证"事件机制不依赖轮询"。
const benchTempPollEnv = "CODEINTEL_WALK_BENCH_TEMP_POLL_MS"

// benchTempEvent 一个临时文件事件（create / grow / unlink / close / closed /
// unobserved）。seen 由事件驱动快照记录，phase 记录该次快照的触发来源。
type benchTempEvent struct {
	TimeMS float64 `json:"t_ms"`
	Target string  `json:"target"`
	Size   int64   `json:"size"`
	State  string  `json:"state"`
	Phase  string  `json:"phase,omitempty"`
}

// selfTempSampler 子进程内采样器（tmpDir 下的 fd 与目录项，含 deleted 目标）。
type selfTempSampler struct {
	tmpDir   string
	interval time.Duration
	poll     bool

	mu          sync.Mutex
	peak        int64
	peakPhase   string
	samples     int
	eventSample int
	maxGap      time.Duration
	last        time.Time
	start       time.Time
	prev        map[string]int64
	prevDeleted map[string]bool
	// onDiskSeen 目录里出现过的名字（含已关闭后仍留在目录里的短生命周期文件）。
	onDiskSeen map[string]int64
	// sized 曾经取得过尺寸的名字/最大字节数（历史，不随文件消失删除）——
	// 用来区分"整个存在期都没被看到"（unobserved）与"采样间隔内可能增长"
	// （仍按观测峰值报，属既有采样口径）。
	sized map[string]int64
	// watchedNames 事件监听报告的新建名字（尚未在目录/fd 快照里核对）。
	watchedNames map[string]bool
	// lost 事件里见过、但从未取得尺寸的名字（关闭 + unlink 都发生在两次
	// 采样之间）——这些文件的真实峰值不可知。
	lost     map[string]bool
	lostN    int
	events   []benchTempEvent
	note     string
	watchErr string
}

func newSelfTempSampler(tmpDir string) *selfTempSampler {
	interval := 250 * time.Microsecond
	poll := true
	if v := os.Getenv(benchTempPollEnv); v != "" {
		if ms, err := strconv.Atoi(v); err == nil && ms >= 0 {
			interval = time.Duration(ms) * time.Millisecond
			poll = ms > 0
		}
	}
	return &selfTempSampler{tmpDir: tmpDir, interval: interval, poll: poll,
		prev: map[string]int64{}, prevDeleted: map[string]bool{},
		onDiskSeen: map[string]int64{}, sized: map[string]int64{},
		watchedNames: map[string]bool{},
		lost: map[string]bool{},
		start: time.Now()}
}

// stats 汇总（调用方持有锁）。
func (s *selfTempSampler) statsLocked() (peak int64, phase string, samples, eventSamples, lostN int,
	maxGap time.Duration, note, watchErr string, lost []string) {
	lost = make([]string, 0, len(s.lost))
	for name := range s.lost {
		lost = append(lost, name)
	}
	sort.Strings(lost)
	return s.peak, s.peakPhase, s.samples, s.eventSample, s.lostN,
		s.maxGap, s.note, s.watchErr, lost
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
	ts := float64(now.Sub(s.start).Microseconds()) / 1000

	cur, curDeleted, total, err := s.liveFds()
	if err != nil {
		s.note = err.Error()
		return
	}
	// 目录枚举对事件快照同样必须做：短生命周期文件可能已经关闭（fd 消失）
	// 但仍留在目录里，只能按路径 stat 到大小。
	s.scanDir(ts, phase, cur, &total)
	if phase == "event" {
		s.eventSample++
	}

	for target, size := range cur {
		if size > s.sized[target] {
			s.sized[target] = size
		}
		old, ok := s.prev[target]
		switch {
		case !ok:
			s.appendEvent(ts, phase, target, size, "create")
			if curDeleted[target] {
				s.appendEvent(ts, phase, target, size, "unlink")
			}
		default:
			if size > old {
				s.appendEvent(ts, phase, target, size, "grow")
			}
			if curDeleted[target] && !s.prevDeleted[target] {
				s.appendEvent(ts, phase, target, size, "unlink")
			}
		}
	}
	for target, old := range s.prev {
		if _, ok := cur[target]; ok {
			continue
		}
		s.appendEvent(ts, phase, target, old, "close")
	}
	s.prev = cur
	s.prevDeleted = curDeleted
	s.samples++
	if total > s.peak {
		s.peak, s.peakPhase = total, phase
	}
	s.note = ""
}

// liveFds 统计 /proc/self/fd 中目标落在 tmpDir 的常规文件（含 deleted）。
func (s *selfTempSampler) liveFds() (map[string]int64, map[string]bool, int64, error) {
	fdDir := "/proc/self/fd"
	entries, err := os.ReadDir(fdDir)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("read %s: %w", fdDir, err)
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
	return cur, curDeleted, total, nil
}

// scanDir 枚举 TMPDIR 目录项：把"两次轮询之间创建、扩容、关闭（但仍在目录
// 里）"的短生命周期文件计入本次快照；对事件里见过、却既不在 fd 也不在目录
// 里的名字记 unobserved（峰值不可知）。
func (s *selfTempSampler) scanDir(ts float64, phase string, cur map[string]int64, total *int64) {
	entries, err := os.ReadDir(s.tmpDir)
	if err != nil {
		return
	}
	present := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := filepath.Join(s.tmpDir, e.Name())
		fi, err := os.Stat(name)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		present[name] = true
		if _, live := cur[name]; live {
			continue // 已按 fd 计入（可能比目录 stat 更大：写入中）
		}
		size := fi.Size()
		old, seen := s.onDiskSeen[name]
		switch {
		case !seen:
			s.appendEvent(ts, phase, name, size, "closed")
		case size > old:
			s.appendEvent(ts, phase, name, size, "grow")
		}
		s.onDiskSeen[name] = size
		s.sized[name] = size
		*total += size
	}
	for name := range s.onDiskSeen {
		if present[name] {
			continue
		}
		if _, live := cur[name]; live {
			continue
		}
		delete(s.onDiskSeen, name)
	}
	// 事件里见过、现在既无 fd 也不在目录里、且**从未取得过尺寸**：整个
	// 存在期都落在两次采样之间（创建/扩容/关闭 + unlink），真实峰值不可知。
	for name := range s.watchedNames {
		if present[name] {
			continue
		}
		if _, live := cur[name]; live {
			continue
		}
		if _, seen := s.sized[name]; seen {
			continue
		}
		if s.lost[name] {
			continue
		}
		s.lost[name] = true
		s.lostN++
		s.appendEvent(ts, phase, name, 0, "unobserved")
	}
	clear(s.watchedNames)
}

// addWatched 记录事件监听报告的名字（scanDir 消费后清空）。
func (s *selfTempSampler) addWatched(name string) {
	if name == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.watchedNames[name] = true
}

// loop 固定间隔采样直到 stop（poll=false 时只等 stop）。
func (s *selfTempSampler) loop(stop <-chan struct{}) {
	if !s.poll {
		<-stop
		return
	}
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			s.snapshot("poll")
		}
	}
}
