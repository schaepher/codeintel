package progress

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
)

// ttyRedrawGap 活动行重绘最小间隔（构建期 Advance 可能很密集）。
const ttyRedrawGap = 50 * time.Millisecond

// TTY codegraph 风格步骤列表（单活动行）。
//
// 模型：最新 Begin 的步骤占据"活动行"（用 \r + ESC[K 原地重绘，可显示
// 进度条）；步骤 End 时打印静态行 `◆ name — 耗时`（失败 ✗），并把活动行
// 交给仍在进行的最近一个步骤。并行适配器（scip/ast/git/ssa）因此表现为
// "按完成顺序的静态行 + 一个活动行"——诚实反映并行，不编造顺序。
type TTY struct {
	mu    sync.Mutex
	w     io.Writer
	title string
	start time.Time
	begun bool
	// Gap 活动行重绘最小间隔（0=不节流；测试用）。
	Gap time.Duration

	active []*ttyStep
	live   *ttyStep
	last   time.Time
}

type ttyStep struct {
	name  string
	depth int
	total int
	done  int
	start time.Time
}

var _ domain.Progress = (*TTY)(nil)

// NewTTY 构造 TTY 渲染器（title 为列表标题，空则用 "构建索引"）。
func NewTTY(w io.Writer, title string) *TTY {
	if title == "" {
		title = "构建索引"
	}
	return &TTY{w: w, title: title, Gap: ttyRedrawGap}
}

func (t *TTY) Begin(name string, depth, total int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.headerLocked()
	t.clearLiveLocked()
	s := &ttyStep{name: name, depth: depth, total: total, start: time.Now()}
	t.active = append(t.active, s)
	t.live = s
	t.drawLocked(s)
}

func (t *TTY) Advance(name string, done int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.findLocked(name)
	if s == nil {
		return
	}
	s.done = done
	if s != t.live {
		return // 非活动行：状态留存，等 End 或重新成为活动行时渲染
	}
	if done < s.total && t.Gap > 0 && time.Since(t.last) < t.Gap {
		return // 节流（收尾/满进度总是重绘）
	}
	t.drawLocked(s)
}

func (t *TTY) End(name string, elapsed time.Duration, err error) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.findLocked(name)
	if s == nil {
		return
	}
	t.removeLocked(s)
	if t.live == s {
		t.clearLiveLocked()
		t.live = nil
	}
	mark, tail := "◆", ""
	if err != nil {
		mark, tail = "✗", "（失败: "+err.Error()+"）"
	}
	if s.total > 0 {
		tail = fmt.Sprintf("（%d/%d）", s.done, s.total) + tail
	}
	writeLine(t.w, "│%s%s %s — %s%s\n", indent(s.depth), mark, s.name, dur(elapsed), tail)
	// 仍有进行中的步骤 → 最近一个重新占据活动行（并行适配器场景）
	if len(t.active) > 0 {
		t.live = t.active[len(t.active)-1]
		t.drawLocked(t.live)
	}
}

func (t *TTY) Finish() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.clearLiveLocked()
	t.live = nil
	if !t.begun {
		return
	}
	writeLine(t.w, "└  完成（总耗时 %s）\n", dur(time.Since(t.start)))
}

// headerLocked 首个步骤时打印列表头（codegraph 的 `┌  Indexing project`）。
func (t *TTY) headerLocked() {
	if t.begun {
		return
	}
	t.begun = true
	t.start = time.Now()
	writeLine(t.w, "┌  %s\n", t.title)
}

// drawLocked 重绘活动行（无换行——下一行原地覆盖）。
func (t *TTY) drawLocked(s *ttyStep) {
	if s == nil {
		return
	}
	line := "│" + indent(s.depth) + "· " + s.name
	if b := bar(s.done, s.total, barWidth); b != "" {
		line += "  " + b
	} else {
		line += "（" + dur(time.Since(s.start)) + "）"
	}
	writeLine(t.w, "\r\x1b[K%s", line)
	t.last = time.Now()
}

// clearLiveLocked 擦掉活动行（未打印的步骤不残留半行）。
func (t *TTY) clearLiveLocked() {
	if t.live == nil {
		return
	}
	writeLine(t.w, "\r\x1b[K")
	t.last = time.Time{}
}

func (t *TTY) findLocked(name string) *ttyStep {
	for _, s := range t.active {
		if s.name == name {
			return s
		}
	}
	return nil
}

func (t *TTY) removeLocked(target *ttyStep) {
	for i, s := range t.active {
		if s == target {
			t.active = append(t.active[:i], t.active[i+1:]...)
			return
		}
	}
}
