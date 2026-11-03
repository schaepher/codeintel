package action

// U04n：事务槽的克隆/合并/错误绑定辅助。

import (
	"go/ast"
	"go/token"
)

// copyTxMaps 事务槽表（clone 用）。
func (e *seqEnv) copyTxMaps() (map[*ast.Object]seqTxSlot, map[*ast.Object]seqTxErrSlot) {
	return e.copyTx()
}

// mergeTx 分支合并：事务身份/错误槽一致才保留，否则未知。
func (e *seqEnv) mergeTx(branches []*seqEnv) {
	if e == nil || len(branches) == 0 {
		return
	}
	objs := map[*ast.Object]bool{}
	for _, br := range branches {
		if br == nil {
			continue
		}
		for obj := range br.txs {
			objs[obj] = true
		}
		for obj := range br.txErrs {
			objs[obj] = true
		}
	}
	for obj := range objs {
		var firstTx *SeqTxOrigin
		sameTx := true
		var firstErr *SeqTxOpResult
		sameErr := true
		for i, br := range branches {
			if br == nil {
				continue
			}
			txSlot := br.txs[obj]
			errSlot := br.txErrs[obj]
			if i == 0 {
				firstTx, firstErr = txSlot.origin, errSlot.ref
				continue
			}
			if txSlot.origin == nil || firstTx == nil || txSlot.origin.ID != firstTx.ID ||
				txSlot.escaped {
				sameTx = false
			}
			if errSlot.ref == nil || firstErr == nil || errSlot.ref.CallID != firstErr.CallID ||
				errSlot.escaped {
				sameErr = false
			}
		}
		if _, hadTx := e.txs[obj]; hadTx || firstTx != nil {
			if sameTx && firstTx != nil {
				e.setTx(obj, firstTx)
			} else {
				e.markTxEscaped(obj)
			}
		}
		if _, hadErr := e.txErrs[obj]; hadErr || firstErr != nil {
			if sameErr && firstErr != nil {
				e.setTxErr(obj, firstErr)
			} else {
				e.setTxErr(obj, nil)
			}
		}
	}
}

// seqTransactionCondition 条件引用事务错误槽。
func seqTransactionCondition(req CodeSequenceRequest, cond ast.Expr) *SeqTransactionCondition {
	expr, invert := seqUnwrapNegation(cond)
	cmp, ok := expr.(*ast.BinaryExpr)
	if !ok || (cmp.Op != token.EQL && cmp.Op != token.NEQ) {
		return nil
	}
	var ref *SeqTxOpResult
	equalNil := false
	if seqIsNilIdent(cmp.Y) {
		ref = seqTxErrRefOf(req, cmp.X)
		equalNil = true
	} else if seqIsNilIdent(cmp.X) {
		ref = seqTxErrRefOf(req, cmp.Y)
		equalNil = true
	}
	if ref == nil {
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
	return &SeqTransactionCondition{Result: ref, WhenTrue: whenTrue, WhenFalse: whenFalse}
}
