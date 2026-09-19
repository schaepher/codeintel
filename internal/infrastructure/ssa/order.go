package ssa

// Q250：确定性排序助手。
//
// 背景：go2o 同二进制双跑（workers=1）nodes 归一化差异 1670 行、摘要 136 行、
// alias 边 492 行——**与并发无关**（串行照样差），根因是 map 迭代顺序每次
// 随机（Go 按 map 实例播种）。性能治理（Q247–Q249）把函数全集排序后差异
// 降到 47，剩下的是"谁先抢到裸槽位名"（`#t0` vs `#t0@14`）随 map 顺序翻转：
//
//   - alias_compute.go: `for v := range p.fieldValues[funcID]`
//   - alias_pass.go:     `for obj := range p.mayOf(fn, v)`
//
// 这两处都会经 valueNodeID/objectIDOf **认领槽位名**（先到先得），故迭代
// 顺序直接决定节点 ID 与 alias 边朝向。
//
// 处理：把这类"遍历 map → 发射/命名"的循环改为**先按确定键排序再遍历**。
// 排序键取 (Name, 行号, token.Pos, String)——全部由 value 自身内容决定，
// 与迭代/并发/插入顺序无关。两个键完全相同的 value 输出也相同（当前实现
// 本就把它们合并成同一 ID），故不存在不可判定的平局。

import (
	"sort"

	"golang.org/x/tools/go/ssa"
)

// valueOrderKey value 的确定序键。
func valueOrderKey(v ssa.Value) string {
	if v == nil {
		return ""
	}
	line := 0
	if pos := v.Pos(); pos.IsValid() {
		line = int(pos)
	}
	return v.Name() + "\x00" + itoaPad(line) + "\x00" + v.String()
}

// itoaPad 定长十进制（保证字符串比较与数值比较一致）。
func itoaPad(n int) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := ""
	for {
		s = string(rune('0'+n%10)) + s
		n /= 10
		if n == 0 {
			break
		}
	}
	for len(s) < 12 {
		s = "0" + s
	}
	if neg {
		s = "-" + s
	}
	return s
}

// sortedValues 返回按确定键升序排列的副本。
func sortedValues(vs []ssa.Value) []ssa.Value {
	out := make([]ssa.Value, len(vs))
	copy(out, vs)
	sort.Slice(out, func(i, j int) bool { return valueOrderKey(out[i]) < valueOrderKey(out[j]) })
	return out
}

// sortedValueKeys 取出 map 的键并排序（键为 ssa.Value 的 map）。
func sortedValueKeys[V any](m map[ssa.Value]V) []ssa.Value {
	out := make([]ssa.Value, 0, len(m))
	for v := range m {
		out = append(out, v)
	}
	return sortedValues(out)
}
