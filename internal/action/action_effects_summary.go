package action

import (
	"sort"

	"github.com/schaepher/codeintel/internal/domain"
)

// fileOfStrings 文件映射（canonical ID → file）转字符串键。
func fileOfStrings(m map[domain.CanonicalID]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[string(k)] = v
	}
	return out
}

// unionPathWrites 路径写入并集（目标返回槽模式的函数级汇总）。
func unionPathWrites(paths []EffectPath) []DBEffect {
	seen := map[string]bool{}
	out := []DBEffect{}
	for _, p := range paths {
		for _, w := range p.Writes {
			key := w.Function + "\x00" + w.Field + "\x00" + itoaSeq(w.Line)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, w)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Field != out[j].Field {
			return out[i].Field < out[j].Field
		}
		return out[i].Line < out[j].Line
	})
	return out
}

// targetResultSummary 目标模式的函数级返回汇总（路径返回槽并集，不重编号）。
func targetResultSummary(paths []EffectPath, old ResultEffect) ResultEffect {
	seen := map[string]bool{}
	var returns, slots []string
	line := 0
	for _, p := range paths {
		if p.Result == nil {
			continue
		}
		if line == 0 {
			line = p.Result.Line
		}
		for i := range p.Result.Returns {
			key := itoaSeq(i) + "\x00" + p.Result.Returns[i]
			if seen[key] {
				continue
			}
			seen[key] = true
			returns = append(returns, p.Result.Returns[i])
			if i < len(p.Result.Slots) {
				slots = append(slots, p.Result.Slots[i])
			}
		}
	}
	var suspicious []string
	for _, p := range paths {
		if p.Result != nil {
			suspicious = append(suspicious, p.Result.Suspicious...)
		}
	}
	suspicious = dedupStrings(suspicious)
	res := ResultEffect{Returns: returns, Slots: slots, Line: line,
		Verdict: SeqVerdictCorroborated, ErrorVars: old.ErrorVars,
		Suspicious: suspicious, Detail: "目标筛选后的函数级汇总（按路径返回槽）"}
	if len(suspicious) > 0 {
		res.Verdict = SeqVerdictUnverified
		res.Detail = "存在错误被覆盖/被返回 nil 吞掉的形态（可疑，需人工确认）"
	}
	return res
}
