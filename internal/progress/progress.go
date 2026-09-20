// Package progress 进度渲染（Q253）：把 domain.Progress 的调用渲染成
// codegraph 风格的步骤列表（TTY live bar）或逐行日志（非 TTY）。
//
// 形态参考 codegraph（/usr/local/bin/codegraph，Node CLI）实测输出：
//
//	┌  Indexing project
//	│  ◆ Scanning files — 7 found
//	│  · Parsing code  ██████████████████████░░░  86%
//	│  ◆ Resolving refs — done
//	└  Done
//
// 与它的差异：本仓库构建期**适配器并行**（scip/ast/git/ssa），所以采用
// "单活动行"模型——最新开始的步骤占据活动行（可被 \r 重绘），其余步骤在
// End 时按完成顺序打印静态行；不显示 ETA/速率（没有可靠依据）。
//
// 约束：只写 stderr（stdout 是查询结果/--json 契约）。
package progress

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
)

// 模式（--progress）。
const (
	ModeAuto  = "auto"  // 默认：stderr 是字符设备 → TTY live bar，否则逐行
	ModePlain = "plain" // 逐行（agent/日志抓取：无 ANSI、可解析）
	ModeNone  = "none"  // 静默
)

// barWidth 进度条格数（codegraph 同宽）。
const barWidth = 25

// Config 渲染配置。
type Config struct {
	Mode   string // auto|plain|none（空或未知 = auto）
	Prefix string // plain 模式行前缀，如 "[index] 步骤"
	Title  string // TTY 模式标题，如 "构建索引"
	// Detail 可选：plain 模式在耗时后追加的上下文（如 ssa 的 heap 统计），
	// nil 表示无。
	Detail func() string
}

// New 按 Config 构造实现；w 为 nil 或模式 none 时返回静默实现。
func New(w *os.File, cfg Config) domain.Progress {
	switch cfg.Mode {
	case ModeNone:
		return domain.NopProgress{}
	case ModePlain:
		return &Plain{W: w, Prefix: cfg.Prefix, Detail: cfg.Detail}
	case ModeAuto, "":
		if isTerminal(w) {
			return NewTTY(w, cfg.Title)
		}
		return &Plain{W: w, Prefix: cfg.Prefix, Detail: cfg.Detail}
	default:
		// 未知模式按 auto（宽容，避免拼错参数就静默丢失进度）
		return New(w, Config{Mode: ModeAuto, Prefix: cfg.Prefix, Title: cfg.Title, Detail: cfg.Detail})
	}
}

// isTerminal 判断是否字符设备（TTY）。用 Stat 而非引第三方 term 包。
func isTerminal(w *os.File) bool {
	if w == nil {
		return false
	}
	fi, err := w.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// bar 渲染进度条：width 格，实心 █、空心 ░，百分比右对齐 3 格（86%）。
// total<=0 返回空串（无真实分母不画条）。
func bar(done, total, width int) string {
	if total <= 0 {
		return ""
	}
	pct := done * 100 / total
	switch {
	case pct > 100:
		pct = 100
	case pct < 0:
		pct = 0
	}
	filled := pct * width / 100
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled) +
		fmt.Sprintf("  %3d%%", pct)
}

// indent 步骤缩进（depth 0 → 2 空格，每层 +3）。
func indent(depth int) string {
	if depth < 0 {
		depth = 0
	}
	return strings.Repeat("   ", depth) + "  "
}

// dur 统一耗时格式（与既有 [index] 步骤 行一致）。
func dur(d time.Duration) string { return d.Round(time.Millisecond).String() }

// writeLine 写一行（best-effort：渲染失败不影响构建）。
func writeLine(w io.Writer, format string, args ...any) {
	if w == nil {
		return
	}
	_, _ = fmt.Fprintf(w, format, args...)
}
