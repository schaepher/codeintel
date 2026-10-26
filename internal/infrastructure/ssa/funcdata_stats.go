package ssa

// P1-07：funcData 规模观测（只在构建结束算一次，O(函数数)）。
//
// 为什么要它：跨包摘要传播（emitSummaries）的输入是全部函数的 direct
// reads/writes + 静态调用记录（calls 含 argStructPaths/argNames）。这些
// 记录在 finishIndex 结束前必须驻留，是"峰值存活内存 / 固定点工作量"的
// 直接证据——没有这个数字，只能靠猜决定"是否裁剪调用记录"。
//
// 输出字段：funcs（有摘要数据的函数数）、calls（静态调用记录数）、
// arg_paths（调用实参结构体路径条目数）、arg_names（实参名条目数）、
// direct/indirect（直接/间接字段条目数）。

import (
	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

func funcDataStats(fd map[domain.CanonicalID]*funcData) []zap.Field {
	calls, argPaths, argNames, direct, indirect := 0, 0, 0, 0, 0
	for _, d := range fd {
		if d == nil {
			continue
		}
		calls += len(d.calls)
		direct += len(d.directReads) + len(d.directWrites)
		indirect += len(d.indirectWrites)
		for _, c := range d.calls {
			argPaths += len(c.argStructPaths)
			argNames += len(c.argNames)
		}
	}
	return []zap.Field{
		zap.Int("funcs", len(fd)),
		zap.Int("calls", calls),
		zap.Int("arg_paths", argPaths),
		zap.Int("arg_names", argNames),
		zap.Int("direct", direct),
		zap.Int("indirect", indirect),
	}
}
