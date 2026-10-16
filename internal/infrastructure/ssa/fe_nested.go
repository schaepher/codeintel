package ssa

// 匿名嵌套字段的类型路径恢复（Q?）。
//
// 问题：fieldInfo 对具名结构体给出 pkg.Named.field 的 full_path；但匿名
// struct（嵌套字段、容器元素等）没有名称，原实现直接回退变量名派生的
// instance_path——同一字段经不同变量名访问会得到不同 full_path（模块/表
// 关联与跨函数链的键不稳定）。
//
// 做法：沿 SSA 的字段访问（FieldAddr/Field）、解引用（UnOp MUL）、容器
// 索引（IndexAddr/Index，标记 [*]）、切片表达式（Slice/SliceToArrayPointer）
// 向外回溯，跨过"单一赋值的副本"（Alloc 只有一个 Store 来源），直到某个
// 具名结构体类型；把途经的匿名字段名拼成类型路径。无法恢复最近具名类型
// （独立匿名结构体、来源不明）时返回空串，调用方保留原 instance_path 回退。
//
// 该路径只影响 full_path 属性；节点 ID 仍用 instance_path（变量名语义），
// 故不改变去重/合并口径。

import (
	"go/token"
	"go/types"

	"golang.org/x/tools/go/ssa"
)

// maxNestedDepth 防止字段/索引链异常时的无限回溯。
const maxNestedDepth = 12

// nestedTypePath 返回 v 对应值的类型限定路径（不含 v 自身字段）；无法恢复返回空。
func (ext *fieldExtractor) nestedTypePath(v ssa.Value, depth int) string {
	if v == nil || depth > maxNestedDepth {
		return ""
	}
	switch x := v.(type) {
	case *ssa.FieldAddr:
		return joinTypePath(ext.nestedTypePath(x.X, depth+1), fieldNameOf(x.X.Type(), x.Field))
	case *ssa.Field:
		return joinTypePath(ext.nestedTypePath(x.X, depth+1), fieldNameOf(x.X.Type(), x.Field))
	case *ssa.UnOp:
		if x.Op == token.MUL { // 解引用：继续向外
			return ext.nestedTypePath(x.X, depth+1)
		}
	case *ssa.IndexAddr: // 容器元素（切片/数组/map 取址）
		if base := ext.nestedTypePath(x.X, depth+1); base != "" {
			return base + "[*]"
		}
		return ""
	case *ssa.Index: // 数组/字符串/元组取值
		if base := ext.nestedTypePath(x.X, depth+1); base != "" {
			return base + "[*]"
		}
		return ""
	case *ssa.Slice: // 子切片：保持容器层级
		return ext.nestedTypePath(x.X, depth+1)
	case *ssa.SliceToArrayPointer:
		return ext.nestedTypePath(x.X, depth+1)
	case *ssa.Alloc:
		// 遍历元素的单一赋值副本（range 元素变量等）：只有一个 Store 来源
		// 时追溯到来源；多个赋值/无来源 → 不可恢复。
		if src := ext.singleStoredValue(x); src != nil {
			return ext.nestedTypePath(src, depth+1)
		}
		return ""
	}
	return namedStructPath(v.Type())
}

// joinTypePath 拼接类型路径段；base/field 任一为空则不可恢复。
func joinTypePath(base, field string) string {
	if base == "" || field == "" {
		return ""
	}
	return base + "." + field
}

// namedStructPath 具名结构体类型的限定路径（pkg.Named）；非具名 struct 返回空。
func namedStructPath(t types.Type) string {
	named, _ := derefStruct(t)
	if named == nil || named.Obj() == nil || named.Obj().Pkg() == nil {
		return ""
	}
	return named.Obj().Pkg().Path() + "." + named.Obj().Name()
}

// singleStoredValue 返回只有一个 Store 写入的 Alloc 的来源值；否则 nil。
func (ext *fieldExtractor) singleStoredValue(alloc *ssa.Alloc) ssa.Value {
	if ext.fn == nil || alloc == nil {
		return nil
	}
	var found ssa.Value
	n := 0
	for _, b := range ext.fn.Blocks {
		for _, instr := range b.Instrs {
			st, ok := instr.(*ssa.Store)
			if !ok || st.Addr != alloc {
				continue
			}
			n++
			if n > 1 {
				return nil
			}
			found = st.Val
		}
	}
	return found
}
