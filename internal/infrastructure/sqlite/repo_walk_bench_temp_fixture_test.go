package sqlite

// todo23/todo24 14.4-B：临时文件峰值——可控夹具断言精确字节数。
//
// 两种夹具：
//   - hold：查询前创建 4KB、扩展到 64KB，unlink 后继续持有 fd，查询结束后
//     关闭——验证"已 unlink 仍打开"的文件被计入峰值。
//   - ephemeral：只做 create 4KB → grow 64KB → close（文件留在目录里），
//     全程没有轮询/边界快照参与（轮询间隔被显式拉长到 100ms），验证
//     "两次轮询之间创建、扩容并关闭"的短生命周期文件靠事件驱动采样被完整
//     记录，峰值等于真实最大字节数（不是 unavailable，也不是伪造 0）。
//
// 这些断言是 Linux 专用（inotify + /proc/self/fd）；其他平台明确跳过并说明
// 原因，不能把跳过计为采样验收通过。

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

func requireLinuxTempSampling(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skipf("跳过 Linux 专用临时文件采样断言：%s 无 inotify 与 /proc/self/fd，"+
			"生产采集在该平台返回 unavailable（原因随样本输出）", runtime.GOOS)
	}
}

func TestWalkBenchShortQueryTempPeakFixture(t *testing.T) {
	requireLinuxTempSampling(t)
	fx := buildWalkBenchFixture(t, 256)
	out := t.TempDir()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(benchTempFixtureEnv, "1")
	smp := runBenchSample(t, bin, out, fx, 1, benchVariantDirectOneHop, 1, "fixture", 1)
	if smp.Error != "" {
		t.Fatalf("fixture 样本失败: %s（dir=%s）", smp.Error, smp.ErrorDir)
	}
	if smp.TempPeakSource != "child_query_events" {
		t.Errorf("应采用子进程查询期事件驱动采样，got source=%q", smp.TempPeakSource)
	}
	if smp.TempPeakBasis != "exact" {
		t.Errorf("夹具峰值应有精确依据，got basis=%q note=%q", smp.TempPeakBasis, smp.TempPeakNote)
	}
	if !smp.TempPeakBytes.OK || smp.TempPeakBytes.Value != benchFixtureTempFinalSize {
		t.Errorf("临时文件峰值应为精确 %d 字节（不是 unavailable/0），got %+v",
			benchFixtureTempFinalSize, smp.TempPeakBytes)
	}
	if smp.TempSamples == 0 {
		t.Errorf("短查询也应有查询生命周期采样点")
	}
	var sawCreate, sawGrow, sawUnlink bool
	for _, e := range smp.TempEvents {
		switch e.State {
		case "create":
			sawCreate = true
		case "grow":
			sawGrow = true
			if e.Size != benchFixtureTempFinalSize {
				t.Errorf("grow 事件大小应为 %d，got %d", benchFixtureTempFinalSize, e.Size)
			}
		case "unlink":
			sawUnlink = true
		}
	}
	// create/grow 在查询前显式采样；unlink 后仍持有 fd，峰值必须保留。
	// 首次观测就可能已经是扩容后的大小（夹具 create+grow 之间没有采样点），
	// 此时 create 事件本身就是 65536——同样算 grow 已记录。
	if !sawCreate || !sawUnlink {
		t.Errorf("事件记录应含 create/unlink，got %+v", smp.TempEvents)
	}
	if !sawGrow && !benchTempEventsHaveSize(smp.TempEvents, benchFixtureTempFinalSize) {
		t.Errorf("事件未记录到扩容后的 %d 字节，got %+v", benchFixtureTempFinalSize, smp.TempEvents)
	}
}

// TestWalkBenchShortLivedTempPeakFixture：两次轮询之间完成创建、扩容和关闭的
// 短生命周期文件也必须被捕获（事件驱动，而不是靠轮询恰好落在生存期内）。
func TestWalkBenchShortLivedTempPeakFixture(t *testing.T) {
	requireLinuxTempSampling(t)
	fx := buildWalkBenchFixture(t, 256)
	out := t.TempDir()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(benchTempFixtureEnv, "ephemeral")
	// 100ms 轮询：夹具的 create/grow/close 一定落在两次轮询之间（查询本身
	// 只有几毫秒），因此峰值只可能来自 inotify 事件驱动的快照。
	t.Setenv(benchTempPollEnv, "100")
	smp := runBenchSample(t, bin, out, fx, 1, benchVariantDirectOneHop, 1, "short_lived", 1)
	if smp.Error != "" {
		t.Fatalf("short_lived 样本失败: %s（dir=%s）", smp.Error, smp.ErrorDir)
	}
	if smp.TempPeakSource != "child_query_events" {
		t.Fatalf("事件机制应生效，got source=%q watch_note=%q", smp.TempPeakSource, smp.TempWatchNote)
	}
	if smp.TempEventSamples == 0 {
		t.Errorf("事件驱动快照次数应 > 0，got %d", smp.TempEventSamples)
	}
	if smp.TempPeakPhase != "event" {
		t.Errorf("短生命周期文件的峰值应由事件快照记录（不是轮询/边界），got phase=%q", smp.TempPeakPhase)
	}
	if smp.TempPeakBasis != "exact" {
		t.Errorf("应给出精确峰值依据，got basis=%q note=%q", smp.TempPeakBasis, smp.TempPeakNote)
	}
	if !smp.TempPeakBytes.OK || smp.TempPeakBytes.Value != benchFixtureTempFinalSize {
		t.Fatalf("短生命周期文件峰值应为精确 %d 字节（不能 unavailable/0），got %+v",
			benchFixtureTempFinalSize, smp.TempPeakBytes)
	}
	var sawSize bool
	for _, e := range smp.TempEvents {
		if e.Size == benchFixtureTempFinalSize &&
			(e.State == "create" || e.State == "grow" || e.State == "closed") {
			sawSize = true
		}
	}
	if !sawSize {
		t.Errorf("事件应记录到最终大小 %d，got %+v", benchFixtureTempFinalSize, smp.TempEvents)
	}
	if len(smp.TempUnobserved) != 0 {
		t.Errorf("不应有未观测的短生命周期文件，got %v", smp.TempUnobserved)
	}
}

// TestWalkBenchShortQueryNoTempFileIsZero：没有临时文件的短查询 → 有效观测
// 0（不是 unavailable），并标注采样机制。
func TestWalkBenchShortQueryNoTempFileIsZero(t *testing.T) {
	requireLinuxTempSampling(t)
	fx := buildWalkBenchFixture(t, 256)
	out := t.TempDir()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(benchTempFixtureEnv, "")
	smp := runBenchSample(t, bin, out, fx, 1, benchVariantDirectOneHop, 1, "short", 1)
	if smp.Error != "" {
		t.Fatalf("short 样本失败: %s", smp.Error)
	}
	if !strings.HasPrefix(smp.TempPeakSource, "child_query_") {
		t.Fatalf("短查询应有子进程查询期采样，got %q", smp.TempPeakSource)
	}
	if !smp.TempPeakBytes.OK || smp.TempPeakBytes.Value != 0 {
		t.Errorf("无临时文件时应是有效 0（不能 unavailable），got %+v", smp.TempPeakBytes)
	}
}

// TestInotifyNamesParsesShortLivedEvents 低层回归：事件缓冲区解析不丢文件名
// （短生命周期文件唯一能被感知的入口）。
func TestInotifyNamesParsesShortLivedEvents(t *testing.T) {
	requireLinuxTempSampling(t)
	dir := "/tmp/bench"
	name := "etilqs_abc\x00\x00" // 12 字节：NUL 结尾并补齐到 4 字节边界
	buf := make([]byte, 16+len(name))
	putU32 := func(off int, v uint32) { buf[off] = byte(v); buf[off+1] = byte(v >> 8); buf[off+2] = byte(v >> 16); buf[off+3] = byte(v >> 24) }
	putU32(4, 0x100) // IN_CREATE
	putU32(12, uint32(len(name)))
	copy(buf[16:], name)
	got := inotifyNames(dir, buf)
	if len(got) != 1 || got[0] != dir+"/etilqs_abc" {
		t.Fatalf("解析结果不符: %v", got)
	}
}

// benchTempEventsHaveSize 事件里是否记录过某字节数（首次观测即扩容后大小时
// 只会有一条 create 事件）。
func benchTempEventsHaveSize(events []benchTempEvent, size int64) bool {
	for _, e := range events {
		if e.Size == size {
			return true
		}
	}
	return false
}
