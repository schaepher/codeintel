package progress

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
)

// Q253：plain 模式必须与改动前的逐行格式逐字一致（既有日志/工具解析不动）。
func TestPlainOutputFormat(t *testing.T) {
	var buf bytes.Buffer
	p := NewPlain(&buf, "[index] 步骤")
	p.Begin("loadPackages", 0, 0)
	if buf.Len() != 0 {
		t.Fatalf("plain 的 Begin 不应输出：%q", buf.String())
	}
	p.Advance("loadPackages", 1)
	p.End("loadPackages", 124*time.Millisecond+400*time.Microsecond, nil)
	got := buf.String()
	want := regexp.MustCompile(`^\[index\] 步骤 loadPackages（124ms）\n$`)
	if !want.MatchString(got) {
		t.Fatalf("plain 行格式不符：%q", got)
	}
}

// plain：Detail 追加在耗时后（ssa 的 heap 统计走这里）、失败带原因。
func TestPlainDetailAndFailure(t *testing.T) {
	var buf bytes.Buffer
	p := &Plain{W: &buf, Prefix: "[index] ssa 步骤", Detail: func() string { return "heap 170MB/189MB inuse" }}
	p.End("ssautil.Packages", 41*time.Millisecond, nil)
	plain := NewPlain(&buf, "[index] 步骤")
	plain.End("scip", time.Second, errors.New("boom"))
	out := buf.String()
	if !strings.Contains(out, "[index] ssa 步骤 ssautil.Packages（41ms, heap 170MB/189MB inuse）") {
		t.Errorf("detail 未渲染：%q", out)
	}
	if !strings.Contains(out, "（1s，失败: boom）") {
		t.Errorf("失败原因未渲染：%q", out)
	}
}

// 进度条纯函数：宽度/百分比/边界都锁住（codegraph 形态 25 格 + 右对齐 %）。
func TestBarRendering(t *testing.T) {
	cases := []struct {
		done, total, width int
		want               string
	}{
		{0, 7, 25, strings.Repeat("░", 25) + "    0%"},
		{6, 7, 25, strings.Repeat("█", 21) + strings.Repeat("░", 4) + "   85%"},
		{100, 100, 25, strings.Repeat("█", 25) + "  100%"},
		{1, 3, 25, strings.Repeat("█", 8) + strings.Repeat("░", 17) + "   33%"},
		{200, 100, 25, strings.Repeat("█", 25) + "  100%"}, // 越界钳位
		{-1, 100, 25, strings.Repeat("░", 25) + "    0%"},
		{5, 0, 25, ""}, // 无真实分母不画条
	}
	for _, c := range cases {
		if got := bar(c.done, c.total, c.width); got != c.want {
			t.Errorf("bar(%d,%d,%d) = %q, want %q", c.done, c.total, c.width, got, c.want)
		}
	}
}

// TTY：活动行（带条）→ 静态完成行 → 收尾符，且 Begin 打印列表头。
func TestTTYStepListRendering(t *testing.T) {
	var buf bytes.Buffer
	p := NewTTY(&buf, "构建索引")
	p.Gap = 0 // 测试要看到每次 Advance 的重绘（生产默认 50ms 节流）
	p.Begin("loadPackages", 0, 0)
	p.End("loadPackages", 124*time.Millisecond, nil)
	p.Begin("emitFunction 循环", 1, 100)
	p.Advance("emitFunction 循环", 86)
	p.End("emitFunction 循环", 6*time.Second, nil)
	p.Finish()
	plain := stripANSI(buf.String())
	for _, want := range []string{
		"┌  构建索引",
		"│  ◆ loadPackages — 124ms",
		"│     ◆ emitFunction 循环 — 6s（86/100）",
		"└  完成（总耗时",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("缺 %q\n实际:\n%s", want, plain)
		}
	}
	// 活动行必须出现进度条与百分比（86% → 21 格实心）
	if !strings.Contains(plain, strings.Repeat("█", 21)+strings.Repeat("░", 4)+"   86%") {
		t.Errorf("活动行未渲染进度条：%q", plain)
	}
}

// TTY：失败步骤用 ✗ 且带原因，不吞错误。
func TestTTYFailureMarker(t *testing.T) {
	var buf bytes.Buffer
	p := NewTTY(&buf, "构建索引")
	p.Begin("scip", 0, 0)
	p.End("scip", 3*time.Second, errors.New("index failed"))
	out := stripANSI(buf.String())
	if !strings.Contains(out, "│  ✗ scip — 3s（失败: index failed）") {
		t.Fatalf("失败行不符：%q", out)
	}
}

// TTY：并行步骤（同时活跃）——End 一个后，另一个继续占活动行。
func TestTTYParallelStepsKeepLiveLine(t *testing.T) {
	var buf bytes.Buffer
	p := NewTTY(&buf, "构建索引")
	p.Begin("scip", 0, 0)
	p.Begin("ssa", 1, 50) // 抢走活动行
	p.Advance("ssa", 25)
	p.End("scip", time.Second, nil) // scip 先完成 → 静态行
	out := stripANSI(buf.String())
	if !strings.Contains(out, "│  ◆ scip — 1s") {
		t.Errorf("scip 完成行缺失：%q", out)
	}
	if !strings.Contains(out, "50%") {
		t.Errorf("ssa 应继续占活动行（End 后重绘）：%q", out)
	}
}

// 模式选择：none 静默、plain/pane 明确、auto 按字符设备判定。
func TestModeSelection(t *testing.T) {
	if _, ok := New(nil, Config{Mode: ModeNone}).(domain.NopProgress); !ok {
		t.Error("none 应返回静默实现")
	}
	if _, ok := New(nil, Config{Mode: ModePlain, Prefix: "[index] 步骤"}).(*Plain); !ok {
		t.Error("plain 应返回逐行实现")
	}
	// 非字符设备（普通文件）→ auto 退化为逐行
	f, err := os.CreateTemp(t.TempDir(), "not-a-tty")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, ok := New(f, Config{Mode: ModeAuto, Prefix: "[index] 步骤"}).(*Plain); !ok {
		t.Error("auto + 非 TTY 应为逐行实现")
	}
	// 拼错的模式按 auto 处理（不静默丢进度）
	if _, ok := New(f, Config{Mode: "quick"}).(*Plain); !ok {
		t.Error("未知模式应回退 auto")
	}
	// 字符设备 → TTY 实现
	if fi, err := os.Stat("/dev/tty"); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		dev, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
		if err == nil {
			defer dev.Close()
			if _, ok := New(dev, Config{Mode: ModeAuto, Title: "x"}).(*TTY); !ok {
				t.Error("auto + TTY 应为 TTY 实现")
			}
		}
	}
	_ = filepath.Join
}

// 并发：并行适配器会从多 goroutine 调 Advance（-race 下必须无竞态/无 panic）。
func TestTTYConcurrentAdvance(t *testing.T) {
	var buf safeBuffer
	p := NewTTY(&buf, "构建索引")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := "step" + string(rune('a'+i))
			p.Begin(name, i%2, 100)
			for d := 0; d <= 100; d += 10 {
				p.Advance(name, d)
			}
			p.End(name, time.Millisecond, nil)
		}(i)
	}
	wg.Wait()
	p.Finish()
	if buf.Len() == 0 {
		t.Fatal("并发下无输出")
	}
}

// safeBuffer 并发写缓冲（渲染器持锁，但测试的读也要安全）。
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *safeBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

// stripANSI 去掉光标控制序列，便于断言可见文本。
func stripANSI(s string) string {
	re := regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)
	return strings.ReplaceAll(re.ReplaceAllString(s, ""), "\r", "\n")
}
