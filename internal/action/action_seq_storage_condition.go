package action

// U04h：错误变量上的条件（err==nil / nil!=err / !(...)）→ 写入错误槽引用。

import (
	"go/ast"
	"go/token"
)

// seqWriteCondition 条件里的写入错误结果引用（err==nil / nil!=err / !(...)）。
func seqWriteCondition(req CodeSequenceRequest, cond ast.Expr) *SeqWriteCondition {
	expr, invert := seqUnwrapNegation(cond)
	cmp, ok := expr.(*ast.BinaryExpr)
	if !ok {
		return nil
	}
	if cmp.Op != token.EQL && cmp.Op != token.NEQ {
		return nil
	}
	ref, equalNil, ok := seqErrNilComparison(req, cmp)
	if !ok {
		return nil
	}
	whenTrue := SeqWriteResultErrorNil
	if (cmp.Op == token.EQL) != equalNil {
		whenTrue = SeqWriteResultErrorNonNil
	}
	if invert {
		if whenTrue == SeqWriteResultErrorNil {
			whenTrue = SeqWriteResultErrorNonNil
		} else {
			whenTrue = SeqWriteResultErrorNil
		}
	}
	whenFalse := SeqWriteResultErrorNonNil
	if whenTrue == SeqWriteResultErrorNonNil {
		whenFalse = SeqWriteResultErrorNil
	}
	return &SeqWriteCondition{Result: ref, WhenTrue: whenTrue, WhenFalse: whenFalse}
}

// seqUnwrapNegation 去掉括号与逻辑非（invert 表示整体取反）。
func seqUnwrapNegation(e ast.Expr) (ast.Expr, bool) {
	invert := false
	for {
		switch v := e.(type) {
		case *ast.ParenExpr:
			e = v.X
		case *ast.UnaryExpr:
			if v.Op != token.NOT {
				return e, invert
			}
			invert = !invert
			e = v.X
		default:
			return e, invert
		}
	}
}

// seqErrNilComparison 比较式是否为 `err == nil` / `nil != err` 等（返回错误引用）。
func seqErrNilComparison(req CodeSequenceRequest, cmp *ast.BinaryExpr) (*SeqWriteErrorResult, bool, bool) {
	if _, ok := cmp.Y.(*ast.Ident); ok && seqIsNilIdent(cmp.Y) {
		if ref := seqErrRefOf(req, cmp.X); ref != nil {
			return ref, true, true
		}
		return nil, false, false
	}
	if seqIsNilIdent(cmp.X) {
		if ref := seqErrRefOf(req, cmp.Y); ref != nil {
			return ref, true, true
		}
	}
	return nil, false, false
}

// seqIsNilIdent 未遮蔽的预声明 nil。
func seqIsNilIdent(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "nil" && id.Obj == nil
}

// copyErrs 克隆错误结果槽（clone 用）。
func (e *seqEnv) copyErrs() map[*ast.Object]seqErrSlot {
	if e == nil || len(e.errs) == 0 {
		return map[*ast.Object]seqErrSlot{}
	}
	out := make(map[*ast.Object]seqErrSlot, len(e.errs))
	for k, v := range e.errs {
		out[k] = v
	}
	return out
}

// mergeErrs 分支/循环合并：同一写入身份才保留。
func (e *seqEnv) mergeErrs(branches []*seqEnv) {
	if e == nil || len(branches) == 0 {
		return
	}
	if e.errs == nil {
		e.errs = map[*ast.Object]seqErrSlot{}
	}
	objs := map[*ast.Object]bool{}
	for _, br := range branches {
		if br == nil {
			continue
		}
		for obj := range br.errs {
			objs[obj] = true
		}
	}
	for obj := range objs {
		var first *SeqWriteErrorResult
		escaped := false
		at := token.NoPos
		same := true
		for i, br := range branches {
			if br == nil {
				continue
			}
			slot, ok := br.errs[obj]
			if !ok {
				slot = e.errs[obj]
			}
			if slot.escaped {
				escaped = true
				at = slot.escapedAt
			}
			if i == 0 {
				first = slot.ref
				continue
			}
			if !sameSeqWriteResult(first, slot.ref) {
				same = false
			}
		}
		if escaped {
			e.errs[obj] = seqErrSlot{escaped: true, escapedAt: at}
			continue
		}
		if !same {
			e.errs[obj] = seqErrSlot{}
			continue
		}
		e.errs[obj] = seqErrSlot{ref: cloneSeqWriteErrorResult(first)}
	}
}

func sameSeqWriteResult(a, b *SeqWriteErrorResult) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.CallID == b.CallID && a.Slot == b.Slot
}
