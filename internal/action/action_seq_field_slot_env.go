package action

// U04j：字段槽的环境复制/合并与失效清除（从 action_seq_field_slot.go 拆出）。

import (
	"go/ast"
)

// copyBoolMap bool map 副本（环境 clone 用）。
func copyBoolMap(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// copyFieldSlots clone 用。
func (e *seqEnv) copyFieldSlots() map[seqFieldSlotKey]seqFieldSlot {
	out := map[seqFieldSlotKey]seqFieldSlot{}
	for k, v := range e.fields {
		out[k] = v
	}
	return out
}

// mergeFieldSlots 分支合并：任一可达分支缺失/来源不同 → 未知。
func (e *seqEnv) mergeFieldSlots(branches []*seqEnv) {
	if e == nil {
		return
	}
	keys := map[seqFieldSlotKey]bool{}
	for _, br := range branches {
		if br == nil {
			continue
		}
		for k := range br.fields {
			keys[k] = true
		}
	}
	out := map[seqFieldSlotKey]seqFieldSlot{}
	for k := range keys {
		var cand *SeqStorageOrigin
		same := true
		seen := false
		for _, br := range branches {
			if br == nil {
				same = false
				break
			}
			s, ok := br.fields[k]
			if !ok || s.origin == nil {
				same = false
				break
			}
			if !seen {
				cand = s.origin
				seen = true
				continue
			}
			if !sameSeqScalarOrigin(cand, s.origin) {
				same = false
				break
			}
		}
		if same && seen {
			out[k] = seqFieldSlot{origin: cand}
		} else if seen {
			out[k] = seqFieldSlot{}
		}
	}
	e.fields = out
}

// seqFieldSlotOriginOfSelector `record.Kind`（直接字段）→ 字段槽来源。
func seqFieldSlotOriginOfSelector(req CodeSequenceRequest, sel *ast.SelectorExpr) *SeqStorageOrigin {
	o, _ := seqFieldSlotLookupSelector(req, sel)
	return o
}

// seqFieldSlotLookupSelector 字段槽查找；tracked 表示该根属于字段槽机制
// （分配根：无槽即显式未知，不能回退到别的来源）。
func seqFieldSlotLookupSelector(req CodeSequenceRequest, sel *ast.SelectorExpr) (*SeqStorageOrigin, bool) {
	if sel == nil {
		return nil, false
	}
	id, ok := runtimeUnparen(sel.X).(*ast.Ident)
	if !ok || id.Obj == nil {
		return nil, false
	}
	alloc := seqAllocOriginOf(req, id)
	if alloc != nil {
		if !seqAllocMatchesRequest(req, alloc) || req.env.allocEscaped(alloc.ID) {
			return nil, true
		}
		if o, ok := seqFieldSlotGet(req, seqFieldSlotKey{alloc: alloc.ID, field: sel.Sel.Name}); ok {
			return o, true
		}
		return nil, true
	}
	if o, ok := seqFieldSlotGet(req, seqFieldSlotKey{obj: id.Obj, field: sel.Sel.Name}); ok {
		return o, true
	}
	return nil, false
}

// deep 清除（对象/分配）——沿 env 父链失效捕获变量。
func (e *seqEnv) clearObjFieldSlotsDeep(obj *ast.Object) {
	for env := e; env != nil; env = env.parent {
		env.clearObjFieldSlots(obj)
		if env.fieldsUnknown {
			break
		}
	}
}

func (e *seqEnv) clearObjFieldDeep(obj *ast.Object, field string) {
	for env := e; env != nil; env = env.parent {
		env.clearObjField(obj, field)
		if env.fieldsUnknown {
			break
		}
	}
}

func (e *seqEnv) clearAllocFieldSlotsDeep(alloc string) {
	for env := e; env != nil; env = env.parent {
		env.clearAllocFieldSlots(alloc)
		if env.fieldsUnknown {
			break
		}
	}
}

func (e *seqEnv) clearAllocFieldDeep(alloc, field string) {
	for env := e; env != nil; env = env.parent {
		env.clearAllocField(alloc, field)
		if env.fieldsUnknown {
			break
		}
	}
}
