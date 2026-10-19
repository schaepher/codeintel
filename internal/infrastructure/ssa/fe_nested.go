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
	"strconv"

	"golang.org/x/tools/go/ssa"
)

// maxNestedDepth 防止字段/索引链异常时的无限回溯（todo4-1：64，
// 容纳深层匿名嵌套 + 容器元素链）。
const maxNestedDepth = 64

// nestedTypePath 返回 v 对应值的类型限定路径（不含 v 自身字段）；无法恢复返回空。
func (ext *fieldExtractor) nestedTypePath(v ssa.Value, depth int) string {
	if v == nil || depth > maxNestedDepth {
		return ""
	}
	// todo4-1：最近具名结构类型优先——值本身（或其指针）是具名结构体时
	// 直接以它为路径起点，不再向外层匿名包裹回溯（否则 o.In.A.B 会得到
	// Outer.In.A.B，而正确起点是最近的具名类型 Inner）。
	if p := namedStructPath(v.Type()); p != "" {
		return p
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
	return ""
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

// allocStoreResult 单一 Store 来源追溯结果（done=false = 未查过）。
type allocStoreResult struct {
	val  ssa.Value
	done bool
}

// singleStoredValue 返回只有一个 Store 写入的 Alloc 的来源值；否则 nil。
// 结果按 Alloc 缓存在 extractor 上（同一函数内多次字段访问同一副本不重扫）。
// 只扫描 **Alloc 的引用者**（Referrers），不扫描整个函数块——首次查询也不
// 是 O(函数大小)（todo2-3）。
func (ext *fieldExtractor) singleStoredValue(alloc *ssa.Alloc) ssa.Value {
	if ext == nil || alloc == nil {
		return nil
	}
	// 空引用列表直接返回（todo4-1）：不再依赖 ext.fn 非空——引用扫描只需
	// alloc.Referrers()，轻量提取器（fn 为空）也能正确回退。
	refs := alloc.Referrers()
	if refs == nil {
		return nil
	}
	if r, ok := ext.allocStores[alloc]; ok && r.done {
		return r.val
	}
	res := allocStoreResult{done: true}
	n := 0
	for _, instr := range *refs {
		st, ok := instr.(*ssa.Store)
		if !ok || st.Addr != alloc {
			continue
		}
		n++
		if n > 1 {
			res.val = nil
			break
		}
		res.val = st.Val
	}
	if ext.allocStores == nil {
		ext.allocStores = map[*ssa.Alloc]allocStoreResult{}
	}
	ext.allocStores[alloc] = res
	return res.val
}

// valueScopeTag 物理函数的作用域后缀（todo2-6）：闭包（Object()==nil）与
// 同名多个 init 会共享外层 canonical ID，其临时槽位/参数节点必须按**原始
// 作用域**区分——否则不同闭包/init 的 t0 会合并成同一节点（且 emitValue
// 与 alias 两条发射路径会分裂）。普通具名函数返回空（保持既有 ID）。
// 用函数声明位置（闭包 = func 字面量 / init = func 关键字）作稳定判别，
// 不依赖遍历顺序。
func valueScopeTag(prog *ssa.Program, repoPath string, fn *ssa.Function) string {
	if prog == nil || fn == nil {
		return ""
	}
	kind := ""
	switch {
	case fn.Object() == nil:
		// 合成函数（包初始化器 init 无 Pos）没有源码作用域——不加后缀。
		if !fn.Pos().IsValid() {
			return ""
		}
		kind = "closure"
	case fn.Object() != nil:
		// 仅**包级 init**（types.Func 名为 init 且无接收者）——不能用
		// HasPrefix("init")：initPages/initConfig/(T).init 等普通函数会被
		// 误加后缀，导致 alias 与 emitValue 标识分裂。
		obj, _ := fn.Object().(*types.Func)
		if obj == nil || obj.Name() != "init" {
			return ""
		}
		if sig, _ := obj.Type().(*types.Signature); sig != nil && sig.Recv() != nil {
			return ""
		}
		kind = "init"
	default:
		return ""
	}
	p := prog.Fset.PositionFor(fn.Pos(), false)
	if p.Filename == "" {
		return ""
	}
	return "@" + kind + ":" + relPath(repoPath, p.Filename) + ":" + strconv.Itoa(p.Line)
}

// scopeTagFor 值所属物理函数的作用域后缀（v.Parent()，不是 ext.fn——跨函数
// 发射形参时值属于被调函数）。
func (ext *fieldExtractor) scopeTagFor(v ssa.Value) string {
	if ext == nil || v == nil {
		return ""
	}
	return valueScopeTag(ext.prog, ext.repo.Path, v.Parent())
}
