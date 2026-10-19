package ssa

// todo8-1：注册位置去重的平方级比较消除。
//
// 原实现每个 MakeInterface 都对该 (接口, 动态类型) 分组的位置切片做线性
// 扫描（重复注册多时 O(n²)）。现改为：
//   - 分组位置 < 32：保留线性查找（稀疏注册零额外分配）
//   - 达到阈值：为该分组建临时集合，键 = (文件名整数编号, 行号)——值类型、
//     不含指针/哈希，编号与行号不截断
//   - 输出仍按**首次出现顺序**追加（集合只用于成员判断，不遍历集合生成输出）
//
// 状态按构建局部使用（调用方在 collectDispatchRegistrations 内声明），
// 集合/文件编号表都**按需创建**。

import "go/types"

// dispatchLinearDedupLimit 线性去重阈值（达到后为分组建集合）。
const dispatchLinearDedupLimit = 32

// dispatchSiteKey 位置去重键（完整文件编号 + 行号；值类型、无指针）。
type dispatchSiteKey struct {
	fileID int
	line   int
}

// dispatchDedup 去重状态（构建局部；稀疏输入不创建集合/编号表）。
type dispatchDedup struct {
	dense   map[*types.Named]map[string]map[dispatchSiteKey]struct{}
	fileIDs map[string]int
}

// append 把 (file, line) 追加到 regs[iface][dyn]（重复位置跳过）。
func (d *dispatchDedup) append(regs dispatchReg, iface *types.Named, dyn, file string, line int) {
	if regs[iface] == nil {
		regs[iface] = map[string][]dispatchSite{}
	}
	sites := regs[iface][dyn]
	if len(sites) < dispatchLinearDedupLimit {
		for _, old := range sites {
			if old.file == file && old.line == line {
				return
			}
		}
		regs[iface][dyn] = append(sites, dispatchSite{file: file, line: line})
		return
	}
	if d.dense == nil {
		d.dense = map[*types.Named]map[string]map[dispatchSiteKey]struct{}{}
	}
	byDyn := d.dense[iface]
	if byDyn == nil {
		byDyn = map[string]map[dispatchSiteKey]struct{}{}
		d.dense[iface] = byDyn
	}
	set := byDyn[dyn]
	if set == nil {
		set = make(map[dispatchSiteKey]struct{}, len(sites)+1)
		for _, old := range sites {
			set[dispatchSiteKey{fileID: d.fileID(old.file), line: old.line}] = struct{}{}
		}
		byDyn[dyn] = set
	}
	k := dispatchSiteKey{fileID: d.fileID(file), line: line}
	if _, ok := set[k]; ok {
		return
	}
	set[k] = struct{}{}
	regs[iface][dyn] = append(sites, dispatchSite{file: file, line: line})
}

// fileID 完整文件名 → 整数编号（首次分配；不截断）。
func (d *dispatchDedup) fileID(file string) int {
	if d.fileIDs == nil {
		d.fileIDs = map[string]int{}
	}
	if id, ok := d.fileIDs[file]; ok {
		return id
	}
	id := len(d.fileIDs)
	d.fileIDs[file] = id
	return id
}
