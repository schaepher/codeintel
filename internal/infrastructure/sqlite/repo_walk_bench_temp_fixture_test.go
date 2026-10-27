package sqlite

// todo23 14.4-B：短查询临时文件峰值——可控夹具断言精确字节数。
//
// 夹具在子进程查询前创建 4KB 临时文件、扩展到 64KB、unlink 后继续持有 fd
// （查询期间一直可观测），查询结束后关闭；子进程内 250µs 采样 + 查询起止
// 快照必须给出精确峰值 65536（不是 unavailable/0）。

import (
	"os"
	"testing"
)

func TestWalkBenchShortQueryTempPeakFixture(t *testing.T) {
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
	if smp.TempPeakSource != "child_query_poll" {
		t.Errorf("应采用子进程查询期采样，got source=%q", smp.TempPeakSource)
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
	if !sawCreate || !sawGrow || !sawUnlink {
		t.Errorf("事件记录应含 create/grow/unlink，got %+v", smp.TempEvents)
	}
}

// TestWalkBenchShortQueryNoTempFileIsZero：没有临时文件的短查询 → 有效观测
// 0（不是 unavailable），并标注采样机制。
func TestWalkBenchShortQueryNoTempFileIsZero(t *testing.T) {
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
	if smp.TempPeakSource != "child_query_poll" {
		t.Fatalf("短查询应有子进程查询期采样，got %q", smp.TempPeakSource)
	}
	if !smp.TempPeakBytes.OK || smp.TempPeakBytes.Value != 0 {
		t.Errorf("无临时文件时应是有效 0（不能 unavailable），got %+v", smp.TempPeakBytes)
	}
}
