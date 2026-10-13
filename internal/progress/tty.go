package progress

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
)

// ttyRedrawGap 活动块重绘最小间隔（构建期 Advance 可能很密集）。
const ttyRedrawGap = 50 * time.Millisecond

// TTY codegraph 风格步骤列表（**多活动行**）。
//
// 模型：所有进行中的步骤各占一行（各自独立的进度条/已用时），作为一个
// 活动块原地重绘（光标上移 + ESC[2K + 重绘）——并行适配器（scip/ast/
// git/ssa）与 SSA 子步骤同时可见，每行有各自的分母与百分比。步骤 End 时
// 从块中移除并打印静态完成行 `◆ name — 耗时`（失败 ✗），再重绘剩余块。
//
// 无真实分母的步骤（total<=0）显示"进行中 + 已用时"（心跳刷新），
// **不编造假百分比**。
type TTY struct {
	mu    sync.Mutex
	w     io.Writer
	title string
	start time.Time
	begun bool
	// Gap 活动块重绘最小间隔（0=不节流；测试用）。
	Gap time.Duration

	active []*ttyStep
	drawn  int // 当前已绘制的活动块行数（擦除用）
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
	t.eraseBlockLocked()
	t.active = append(t.active, &ttyStep{name: name, depth: depth, total: total, start: time.Now()})
	t.drawBlockLocked()
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
	if done < s.total && t.Gap > 0 && time.Since(t.last) < t.Gap {
		return // 节流（收尾/满进度总是重绘）
	}
	t.eraseBlockLocked()
	t.drawBlockLocked()
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
	t.eraseBlockLocked()
	mark, tail := "◆", ""
	if err != nil {
		mark, tail = "✗", "（失败: "+err.Error()+"）"
	}
	if s.total > 0 {
		tail = fmt.Sprintf("（%d/%d）", s.done, s.total) + tail
	}
	writeLine(t.w, "│%s%s %s — %s%s\n", indent(s.depth), mark, s.name, dur(elapsed), tail)
	t.drawBlockLocked()
}

func (t *TTY) Finish() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.eraseBlockLocked()
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

// drawBlockLocked 重绘整个活动块（每步一行，行尾换行——光标停在块下一行）。
func (t *TTY) drawBlockLocked() {
	for _, s := range t.active {
		line := "│" + indent(s.depth) + "· " + s.name
		if b := bar(s.done, s.total, barWidth); b != "" {
			line += "  " + b
		} else {
			line += "（" + dur(time.Since(s.start)) + "）"
		}
		writeLine(t.w, "%s\n", line)
	}
	t.drawn = len(t.active)
	t.last = time.Now()
}

// eraseBlockLocked 擦掉已绘制的活动块：光标上移 drawn 行后逐行清理，
// 结束时停在块下一行（与 drawBlockLocked 的落点一致）。
func (t *TTY) eraseBlockLocked() {
	if t.drawn <= 0 {
		return
	}
	writeLine(t.w, "\x1b[%dA", t.drawn)
	writeLine(t.w, "%s", strings.Repeat("\x1b[2K\n", t.drawn))
	t.drawn = 0
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
