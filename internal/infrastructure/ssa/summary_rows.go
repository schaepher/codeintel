package ssa

// 摘要行发射与代表选择（从 summary.go 拆出——行数治理）。

import (
	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// emitSummaryRows 发射单个 access_kind 的摘要行（同字段路径去重）。
// Q250：赢家按**内容**决定而非切片顺序——取最早行号，行号相同取
// instance_path 字典序最小。原先"取首条"依赖 entries 的构造顺序（指令序
// + 若干 map 驱动的补录路径），同一次构建两次运行会给出不同的
// instance_path/行号/片段（go2o 双跑实测 90-136 行差异）。
func emitSummaryRows(funcID domain.CanonicalID, accessKind domain.SummaryAccessKind, entries []fieldEntry,
	emit domain.EmitFunc) error {
	logger := zap.L()
	logger.Debug("enter emitSummaryRows")
	defer logger.Debug("exit emitSummaryRows")
	best := map[string]fieldEntry{}
	order := make([]string, 0, len(entries))
	for _, e := range entries {
		prev, ok := best[e.fieldPath]
		if !ok {
			best[e.fieldPath] = e
			order = append(order, e.fieldPath)
			continue
		}
		if earlierEntry(e, prev) {
			best[e.fieldPath] = e
		}
	}
	for _, path := range order {
		e := best[path]
		if err := emit(domain.Item{Summary: &domain.FunctionFieldSummary{
			FunctionID:   funcID,
			AccessKind:   accessKind,
			FieldPath:    e.fieldPath,
			InstancePath: e.instancePath,
			LineStart:    e.line,
			CodeSnippet:  e.snippet,
		}}); err != nil {
			return err
		}
	}
	return nil
}

// earlierEntry 判断 a 是否比 b 更适合作为该字段路径的摘要代表：
// 行号小者优先（无行号排后）→ instance_path 字典序 → callLine → callArg
// → snippet 字典序。**必须是全序**：条目集合来自 map（valuesOf
// 迭代顺序随机），平局若不断定到底会让"候选顺序决定代表片段"
// （R11：go2o 间接写 code_snippet 差异）。
func earlierEntry(a, b fieldEntry) bool {
	al, bl := a.line, b.line
	switch {
	case al > 0 && (bl == 0 || al < bl):
		return true
	case bl > 0 && (al == 0 || bl < al):
		return false
	}
	if a.instancePath != b.instancePath {
		return a.instancePath < b.instancePath
	}
	if a.callLine != b.callLine {
		if a.callLine == 0 {
			return false
		}
		if b.callLine == 0 {
			return true
		}
		return a.callLine < b.callLine
	}
	if a.callArg != b.callArg {
		return a.callArg < b.callArg
	}
	return a.snippet < b.snippet
}
