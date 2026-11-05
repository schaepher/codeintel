package action

// U04j/U04k/U04l：Scan 字段槽（值类型模型字段 / new(T) 分配 + 别名 / 只读
// getter）。
//
// 边界（刻意保守）：
//   - 字段槽只在**当前请求**内有效，按"准确变量声明 + 字段名"或"分配身份 +
//     字段名"隔离；不同类型/变量名不共享值；
//   - 只支持直接内建标量字段（嵌套路径/提升字段/类型别名/自定义 Scanner 不
//     支持）；列来源仍是 read_column + when=scan_success（不是观测值）；
//   - 普通字段写入、整对象覆写、自增自减、方法调用（未证明只读）、捕获写者
//     与调度体读取都使旧列值失效；
//   - 预算：256 字段槽 / 256 根状态 / 4096 指针别名声明 / 4096 分配归属判断；
//     超出时记 scan_field_budget，不保留部分字段来源当作完整结果。

import (
	"go/ast"
	"go/token"
)

// 来源类别与预算。
const (
	SeqOriginAllocation = "allocation"
	SeqDiagFieldBudget  = "scan_field_budget"

	seqFieldSlotMax    = 256
	seqFieldRootMax    = 256
	seqAllocAliasMax   = 4096
	seqAllocCheckMax   = 4096
	seqFieldSlotDepth  = 8
	seqScanFieldUnprov = "field_source_unproven"
)

// SeqAllocation new(T) 的分配身份（指针模型）。
type SeqAllocation struct {
	ID     string `json:"id"`
	TypeID string `json:"type_id,omitempty"`
	Repo   string `json:"repo,omitempty"`
	Entry  string `json:"entry,omitempty"`
	Symbol string `json:"symbol,omitempty"`
	File   string `json:"file,omitempty"`
	Offset int    `json:"offset,omitempty"`
}

// seqFieldSlotKey 字段槽身份（对象声明 / 分配 + 字段名）。
type seqFieldSlotKey struct {
	obj   *ast.Object
	alloc string
	field string
}

// seqFieldSlot 字段槽状态（origin nil = 显式未知）。
type seqFieldSlot struct {
	origin *SeqStorageOrigin
}

// seqFieldSlotBudgetExceeded 预算耗尽：清空字段槽并记诊断（只记一次）。
func seqFieldSlotBudgetExceeded(req CodeSequenceRequest) {
	if req.acc == nil || req.acc.scanFieldBudget {
		return
	}
	req.acc.scanFieldBudget = true
	if req.env != nil {
		req.env.clearAllFieldSlots()
	}
}

// seqFieldSlotGet 读取字段槽（父链；masked 环境返回未知）。
func seqFieldSlotGet(req CodeSequenceRequest, key seqFieldSlotKey) (*SeqStorageOrigin, bool) {
	if req.env == nil || (key.obj == nil && key.alloc == "") || key.field == "" {
		return nil, false
	}
	if req.acc != nil && req.acc.scanFieldBudget {
		return nil, false
	}
	for env := req.env; env != nil; env = env.parent {
		if env.fieldsUnknown {
			// 调度体读取外层字段：该根属于字段槽机制，但当前值未知。
			return nil, true
		}
		if s, ok := env.fields[key]; ok {
			return cloneSeqScalarOrigin(s.origin), true
		}
	}
	return nil, false
}

// seqFieldSlotSet 写字段槽（预算检查；超限清空并拒绝）。
func seqFieldSlotSet(req CodeSequenceRequest, key seqFieldSlotKey, origin *SeqStorageOrigin) bool {
	if req.env == nil || (key.obj == nil && key.alloc == "") || key.field == "" {
		return false
	}
	if req.acc != nil && req.acc.scanFieldBudget {
		return false
	}
	if req.env.fields == nil {
		req.env.fields = map[seqFieldSlotKey]seqFieldSlot{}
	}
	if _, exists := req.env.fields[key]; !exists {
		if len(req.env.fields) >= seqFieldSlotMax {
			seqFieldSlotBudgetExceeded(req)
			return false
		}
	}
	req.env.fields[key] = seqFieldSlot{origin: cloneSeqScalarOrigin(origin)}
	return true
}

// seqFieldSlotRootCount 根状态数量（对象/分配去重）。
func seqFieldSlotRootCount(env *seqEnv) int {
	roots := map[string]bool{}
	for e := env; e != nil; e = e.parent {
		for k := range e.fields {
			if k.alloc != "" {
				roots["alloc:"+k.alloc] = true
			} else if k.obj != nil {
				key := "obj:" + itoaSeq(int(k.obj.Pos()))
				roots[key] = true
			}
		}
	}
	return len(roots)
}

// seqFieldSlotRootKey 变量根的字段槽键（值类型对象 / 分配）。
func seqFieldSlotRootKey(req CodeSequenceRequest, id *ast.Ident) (seqFieldSlotKey, bool) {
	if id == nil || id.Obj == nil || req.env == nil {
		return seqFieldSlotKey{}, false
	}
	if origin, ok := req.env.scalarOriginAt(id.Obj, id.Pos()); ok && origin != nil &&
		origin.Kind == SeqOriginAllocation && origin.Allocation != nil {
		return seqFieldSlotKey{alloc: origin.Allocation.ID}, true
	}
	return seqFieldSlotKey{obj: id.Obj}, true
}

// seqAllocOriginOf 标识符当前是否持有某个分配（指针模型）。
func seqAllocOriginOf(req CodeSequenceRequest, id *ast.Ident) *SeqAllocation {
	if id == nil || id.Obj == nil || req.env == nil {
		return nil
	}
	origin, ok := req.env.scalarOriginAt(id.Obj, id.Pos())
	if !ok || origin == nil || origin.Kind != SeqOriginAllocation {
		return nil
	}
	return origin.Allocation
}

// seqAllocOriginForScan Scan 目标位置分配来源（忽略本次调用里的地址传递）。
func seqAllocOriginForScan(req CodeSequenceRequest, id *ast.Ident, callPos token.Pos) *SeqAllocation {
	if id == nil || id.Obj == nil || req.env == nil {
		return nil
	}
	slot, ok := req.env.scalarSlot(id.Obj)
	if !ok || slot.origin == nil || slot.origin.Kind != SeqOriginAllocation ||
		slot.origin.Allocation == nil {
		return nil
	}
	if slot.escaped {
		if slot.escapedAt == token.NoPos || slot.escapedAt < callPos {
			return nil
		}
	}
	return slot.origin.Allocation
}

// seqAllocMatchesRequest 分配必须属于当前仓库/入口（不能跨函数体借用）。
func seqAllocMatchesRequest(req CodeSequenceRequest, alloc *SeqAllocation) bool {
	if alloc == nil {
		return false
	}
	if alloc.Repo != "" && req.RepoAbs != "" && alloc.Repo != req.RepoAbs {
		return false
	}
	if alloc.Entry != "" && req.entryID != "" && alloc.Entry != req.entryID {
		return false
	}
	return true
}

// seqAllocEscaped 分配已经传出（全局/容器/未知调用/捕获等）→ 之后不能重新
// 当作私有对象。
func (e *seqEnv) allocEscaped(id string) bool {
	for env := e; env != nil; env = env.parent {
		if env.escapedAllocs[id] {
			return true
		}
	}
	return false
}

// markAllocEscaped 标记分配传出并清掉它的字段槽。
func seqMarkAllocEscaped(req CodeSequenceRequest, alloc *SeqAllocation) {
	if req.env == nil || alloc == nil || alloc.ID == "" {
		return
	}
	if req.env.escapedAllocs == nil {
		req.env.escapedAllocs = map[string]bool{}
	}
	req.env.escapedAllocs[alloc.ID] = true
	req.env.clearAllocFieldSlots(alloc.ID)
}

// clearAllFieldSlots 清空本层字段槽（预算耗尽/整函数失效用）。
func (e *seqEnv) clearAllFieldSlots() {
	if e == nil {
		return
	}
	e.fields = map[seqFieldSlotKey]seqFieldSlot{}
}

// clearObjFieldSlots 清空某对象的全部字段槽。
func (e *seqEnv) clearObjFieldSlots(obj *ast.Object) {
	if e == nil || obj == nil {
		return
	}
	for k := range e.fields {
		if k.obj == obj {
			delete(e.fields, k)
		}
	}
}

// clearAllocFieldSlots 清空某分配的全部字段槽（所有准确别名共享）。
func (e *seqEnv) clearAllocFieldSlots(alloc string) {
	if e == nil || alloc == "" {
		return
	}
	for k := range e.fields {
		if k.alloc == alloc {
			delete(e.fields, k)
		}
	}
}

// clearObjField 清空某对象的单个字段槽。
func (e *seqEnv) clearObjField(obj *ast.Object, field string) {
	if e == nil || obj == nil {
		return
	}
	delete(e.fields, seqFieldSlotKey{obj: obj, field: field})
}

// clearAllocField 清空某分配的单个字段槽。
func (e *seqEnv) clearAllocField(alloc, field string) {
	if e == nil || alloc == "" {
		return
	}
	delete(e.fields, seqFieldSlotKey{alloc: alloc, field: field})
}
