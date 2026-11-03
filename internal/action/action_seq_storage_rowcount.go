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

// seqRowCondition 行数变量与零值的比较条件（右侧/左侧零值均可、支持取反）。
func seqRowCondition(req CodeSequenceRequest, cond ast.Expr) *SeqRowCountCondition {
	expr, invert := seqUnwrapNegation(cond)
	cmp, ok := expr.(*ast.BinaryExpr)
	if !ok {
		return nil
	}
	ref, op, ok := seqRowZeroComparison(req, cmp)
	if !ok {
		return nil
	}
	trueRes, falseRes := seqRowsCompareOutcome(op)
	if invert {
		trueRes, falseRes = falseRes, trueRes
	}
	return &SeqRowCountCondition{Result: ref, WhenTrue: trueRes, WhenFalse: falseRes}
}

// seqRowZeroComparison 比较式是否为 row <op> 0（或 0 <op> row）。
func seqRowZeroComparison(req CodeSequenceRequest, cmp *ast.BinaryExpr) (*SeqORMRowCountResult, token.Token, bool) {
	if isZeroLiteral(cmp.Y) {
		if ref := seqRowsRefOf(req, cmp.X); ref != nil {
			return ref, cmp.Op, true
		}
	}
	if isZeroLiteral(cmp.X) {
		if ref := seqRowsRefOf(req, cmp.Y); ref != nil {
			return ref, seqFlipCompareOp(cmp.Op), true
		}
	}
	return nil, 0, false
}

// seqRowsRefOf 表达式的行数来源（变量槽优先，其次已证明的 write_row_count 标量来源）。
func seqRowsRefOf(req CodeSequenceRequest, e ast.Expr) *SeqORMRowCountResult {
	id, ok := e.(*ast.Ident)
	if !ok || id.Obj == nil || req.env == nil {
		return nil
	}
	if ref := req.env.rowsResultOf(id.Obj, id.Pos()); ref != nil {
		return ref
	}
	origin, found := req.env.scalarOriginAt(id.Obj, id.Pos())
	if !found || origin == nil || origin.RowCount == nil {
		return nil
	}
	return cloneSeqRowCount(origin.RowCount)
}

func isZeroLiteral(e ast.Expr) bool {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.INT {
		return false
	}
	return lit.Value == "0"
}

// seqFlipCompareOp 左右交换后的运算符。
func seqFlipCompareOp(op token.Token) token.Token {
	switch op {
	case token.LSS:
		return token.GTR
	case token.GTR:
		return token.LSS
	case token.LEQ:
		return token.GEQ
	case token.GEQ:
		return token.LEQ
	}
	return op
}

// seqRowsCompareOutcome 运算符 → 真假结果名。
func seqRowsCompareOutcome(op token.Token) (string, string) {
	switch op {
	case token.GTR:
		return SeqRowsGTZero, SeqRowsLEZero
	case token.LEQ:
		return SeqRowsLEZero, SeqRowsGTZero
	case token.GEQ:
		return SeqRowsGEZero, SeqRowsLTZero
	case token.LSS:
		return SeqRowsLTZero, SeqRowsGEZero
	case token.EQL:
		return SeqRowsEQZero, SeqRowsNEZero
	case token.NEQ:
		return SeqRowsNEZero, SeqRowsEQZero
	}
	return "", ""
}
