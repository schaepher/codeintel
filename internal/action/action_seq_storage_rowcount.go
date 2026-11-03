package action

// U04j：XORM 影响行数返回槽（slot 0）与零值比较条件。
//
// 行数来源只建立在**已证明 (int64,error) 签名**的 XORM 写入方法上；条件只
// 描述符号关系，不证明 error=nil / Commit 成功 / 记录存在。

import (
	"go/ast"
	"go/token"
)

// 行数条件结果。
const (
	SeqRowsGTZero = "rows_gt_zero"
	SeqRowsLEZero = "rows_le_zero"
	SeqRowsGEZero = "rows_ge_zero"
	SeqRowsLTZero = "rows_lt_zero"
	SeqRowsEQZero = "rows_eq_zero"
	SeqRowsNEZero = "rows_ne_zero"
)

// SeqORMRowCountResult 写入影响行数返回槽（与错误槽共享 call_id）。
type SeqORMRowCountResult struct {
	Write *SeqWriteErrorResult `json:"write"`
	Slot  int                  `json:"slot"`
	// Access U04m：计数来源调用（`(Result).RowsAffected` 等），ORM 行数槽为空。
	Access *SeqRowCountAccess `json:"access,omitempty"`
}

// SeqRowCountAccess 影响行数的来源调用（准确 symbol/位置/独立错误槽）。
type SeqRowCountAccess struct {
	Symbol       string `json:"symbol"`
	File         string `json:"file,omitempty"`
	Offset       int    `json:"offset,omitempty"`
	ErrorSlot    int    `json:"error_slot"`
	ErrorOutcome string `json:"error_outcome,omitempty"`
}

// SeqSQLResult 标准库 Exec 的返回槽 0（sql.Result）来源。
type SeqSQLResult struct {
	Write *SeqWriteErrorResult `json:"write"`
	Slot  int                  `json:"slot"`
}

// SeqRowCountCondition 行数比较条件。
type SeqRowCountCondition struct {
	Result    *SeqORMRowCountResult `json:"result"`
	WhenTrue  string                `json:"when_true"`
	WhenFalse string                `json:"when_false"`
}

// seqRowSlot 行数结果变量槽（覆盖/逃逸/合流失效）。
type seqRowSlot struct {
	ref       *SeqORMRowCountResult
	escaped   bool
	escapedAt token.Pos
}

// seqRowsResultOf 变量的行数来源（按使用位置判断逃逸）。
func (e *seqEnv) rowsResultOf(obj *ast.Object, pos token.Pos) *SeqORMRowCountResult {
	if e == nil || obj == nil {
		return nil
	}
	for env := e; env != nil; env = env.parent {
		if slot, ok := env.rows[obj]; ok {
			if slot.escaped {
				if pos == token.NoPos || slot.escapedAt == token.NoPos || slot.escapedAt <= pos {
					return nil
				}
			}
			return cloneSeqRowCount(slot.ref)
		}
	}
	return nil
}

func (e *seqEnv) setRows(obj *ast.Object, ref *SeqORMRowCountResult) {
	if e == nil || obj == nil {
		return
	}
	if e.rows == nil {
		e.rows = map[*ast.Object]seqRowSlot{}
	}
	if slot, ok := e.rows[obj]; ok && slot.escaped {
		e.rows[obj] = seqRowSlot{escaped: true, escapedAt: slot.escapedAt}
		return
	}
	e.rows[obj] = seqRowSlot{ref: cloneSeqRowCount(ref)}
}

func (e *seqEnv) markRowsEscaped(obj *ast.Object, pos token.Pos) {
	if e == nil || obj == nil {
		return
	}
	if e.rows == nil {
		e.rows = map[*ast.Object]seqRowSlot{}
	}
	slot := e.rows[obj]
	slot.escaped = true
	slot.escapedAt = pos
	e.rows[obj] = slot
}

func (e *seqEnv) hasRowsSlot(obj *ast.Object) bool {
	for env := e; env != nil; env = env.parent {
		if _, ok := env.rows[obj]; ok {
			return true
		}
	}
	return false
}

func cloneSeqRowCount(r *SeqORMRowCountResult) *SeqORMRowCountResult {
	if r == nil {
		return nil
	}
	cp := *r
	if r.Write != nil {
		w := *r.Write
		cp.Write = &w
	}
	if r.Access != nil {
		a := *r.Access
		cp.Access = &a
	}
	return &cp
}

// copyRows 克隆行数槽（clone 用）。
func (e *seqEnv) copyRows() map[*ast.Object]seqRowSlot {
	if e == nil || len(e.rows) == 0 {
		return map[*ast.Object]seqRowSlot{}
	}
	out := make(map[*ast.Object]seqRowSlot, len(e.rows))
	for k, v := range e.rows {
		out[k] = v
	}
	return out
}

// mergeRows 分支/循环合并：同一写入身份才保留。
func (e *seqEnv) mergeRows(branches []*seqEnv) {
	if e == nil || len(branches) == 0 {
		return
	}
	if e.rows == nil {
		e.rows = map[*ast.Object]seqRowSlot{}
	}
	objs := map[*ast.Object]bool{}
	for _, br := range branches {
		if br == nil {
			continue
		}
		for obj := range br.rows {
			objs[obj] = true
		}
	}
	for obj := range objs {
		var first *SeqORMRowCountResult
		same := true
		escaped := false
		at := token.NoPos
		for i, br := range branches {
			if br == nil {
				continue
			}
			slot, ok := br.rows[obj]
			if !ok {
				slot = e.rows[obj]
			}
			if slot.escaped {
				escaped = true
				at = slot.escapedAt
			}
			if i == 0 {
				first = slot.ref
				continue
			}
			if !sameSeqRowCount(first, slot.ref) {
				same = false
			}
		}
		if escaped {
			e.rows[obj] = seqRowSlot{escaped: true, escapedAt: at}
			continue
		}
		if !same {
			e.rows[obj] = seqRowSlot{}
			continue
		}
		e.rows[obj] = seqRowSlot{ref: cloneSeqRowCount(first)}
	}
}

func sameSeqRowCount(a, b *SeqORMRowCountResult) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a.Slot != b.Slot {
		return false
	}
	if a.Write == nil || b.Write == nil {
		return a.Write == nil && b.Write == nil
	}
	return a.Write.CallID == b.Write.CallID
}
