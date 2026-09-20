package progress

import (
	"io"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
)

// Plain 逐行渲染：每步结束打一行 `[index] 步骤 <name>（<耗时>）`——
// **与 Q253 之前的 stderr 格式逐字一致**（既有日志检索/工具解析不受影响）。
// Begin/Advance/Finish 均为空操作（逐行模式不打中间进度）。
type Plain struct {
	W      io.Writer
	Prefix string
	// Detail 可选：耗时后追加的上下文（ssa 的 heap 统计），nil 无。
	Detail func() string
}

var _ domain.Progress = (*Plain)(nil)

func (p *Plain) Begin(string, int, int) {}

func (p *Plain) Advance(string, int) {}

func (p *Plain) Finish() {}

func (p *Plain) End(name string, elapsed time.Duration, err error) {
	if p == nil {
		return
	}
	detail := ""
	if p.Detail != nil {
		if s := p.Detail(); s != "" {
			detail = ", " + s
		}
	}
	fail := ""
	if err != nil {
		fail = "，失败: " + err.Error()
	}
	writeLine(p.W, "%s %s（%s%s%s）\n", p.Prefix, name, dur(elapsed), detail, fail)
}

// NewPlain 逐行渲染（nil w 安全）。
func NewPlain(w io.Writer, prefix string) *Plain {
	return &Plain{W: w, Prefix: prefix}
}
