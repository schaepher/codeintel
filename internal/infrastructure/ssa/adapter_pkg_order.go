package ssa

// 包顺序辅助（从 adapter_index.go 拆出——行数治理）。

import (
	"sort"

	"golang.org/x/tools/go/ssa"
)

// sortedPkgOrder 包按函数数降序（并行块顺序：大包先发，负载更均衡）。
func sortedPkgOrder(byPkg map[string][]*ssa.Function) []string {
	order := make([]string, 0, len(byPkg))
	for p := range byPkg {
		order = append(order, p)
	}
	sort.Slice(order, func(i, j int) bool {
		return len(byPkg[order[i]]) > len(byPkg[order[j]])
	})
	return order
}
