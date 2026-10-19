package ssa

// 包顺序辅助（从 adapter_index.go 拆出——行数治理）。

import (
	"sort"

	"golang.org/x/tools/go/packages"
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

// indexPkgsByPath 包路径索引（todo8-8）：同一路径多个包对象时保留**原切片
// 第一个**（与旧内层线性查找的 first-match 一致）；缺失路径查不到（nil）。
func indexPkgsByPath(pkgs []*packages.Package) map[string]*packages.Package {
	idx := make(map[string]*packages.Package, len(pkgs))
	for _, p := range pkgs {
		if p == nil {
			continue
		}
		if _, ok := idx[p.PkgPath]; !ok {
			idx[p.PkgPath] = p
		}
	}
	return idx
}
