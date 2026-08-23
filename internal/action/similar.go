package action

// Q244 相似名提示：表名/符号名拼错时给出候选（「你是要找 X 吗」）。
// 前缀命中优先（用户通常记得开头），再按 Levenshtein 距离 ≤2 排序。

import "sort"

// similarCandidates 从候选池中取与输入最相似的名称（最多 limit 个）：
//  1. 前缀匹配（strings.HasPrefix）优先
//  2. 编辑距离 ≤2 的按距离升序
//
// 返回按（前缀优先，距离升序）排序的名称列表。
func similarCandidates(input string, pool []string, limit int) []string {
	type scored struct {
		name string
		dist int // -1 = 前缀命中
	}
	var hits []scored
	for _, p := range pool {
		if p == input {
			continue // 精确命中不在此列（调用方已处理）
		}
		if len(input) >= 2 && len(p) >= len(input) && p[:len(input)] == input {
			hits = append(hits, scored{p, -1})
			continue
		}
		if d := levenshtein(input, p); d <= 2 {
			hits = append(hits, scored{p, d})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].dist != hits[j].dist {
			return hits[i].dist < hits[j].dist
		}
		return hits[i].name < hits[j].name
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.name)
	}
	return out
}

// levenshtein 编辑距离（小字符串，O(n*m)）。
func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		cur := make([]int, lb+1)
		cur[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[lb]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}
