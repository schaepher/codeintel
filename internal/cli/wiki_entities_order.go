package cli

import (
	"sort"

	"github.com/schaepher/codeintel/internal/domain"
)

// chainLineNum 调用边行号（metadata.line_num——AST 发射时记录调用
// 位置；缺失返回 -1）。
func chainLineNum(f *domain.Fact) int {
	if v, ok := f.Metadata["line_num"]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		case int64:
			return int(n)
		}
	}
	return -1
}
// sortChainByCallLine 调用链按源码调用行号排序（R13）：深度 1（入口
// 直接调用）按入口内行号；深度 2（被调者内部）按被调者在入口中的
// 调用位置 + 内部行号——还原源码书写顺序（"顺序与代码一一对应"）。
// 无行号边 fallback 排最后。
func sortChainByCallLine(entryID string, facts []*domain.Fact) []domain.WikiSeqStep {
	pos := map[string]int{}
	for _, f := range facts {
		if string(f.SourceID) == entryID {
			if ln := chainLineNum(f); ln > 0 {
				pos[string(f.TargetID)] = ln
			}
		}
	}
	const noLine = 1 << 30
	key := func(f *domain.Fact) (int, int) {

		if string(f.SourceID) == entryID {
			if ln := chainLineNum(f); ln > 0 {
				return ln, 0
			}
			return noLine, 0
		}
		p, ok := pos[string(f.SourceID)]
		if !ok {
			return noLine, noLine
		}
		if ln := chainLineNum(f); ln > 0 {
			return p, ln
		}
		return p, noLine
	}
	sorted := append([]*domain.Fact(nil), facts...)
	sort.SliceStable(sorted, func(i, j int) bool {
		ki1, ki2 := key(sorted[i])
		kj1, kj2 := key(sorted[j])
		if ki1 != kj1 {
			return ki1 < kj1
		}
		return ki2 < kj2
	})
	var out []domain.WikiSeqStep
	seen := map[[2]string]bool{}
	for _, f := range sorted {
		caller, callee := shortSymbolNameID(string(f.SourceID)), shortSymbolNameID(string(f.TargetID))
		if seen[[2]string{caller, callee}] {
			continue
		}
		seen[[2]string{caller, callee}] = true
		out = append(out, domain.WikiSeqStep{Caller: caller, Callee: callee})
	}
	return out
}
