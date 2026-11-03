package action

// U04h：错误结果变量槽（覆盖/逃逸/合流失效）。

import (
	"go/ast"
	"go/token"
)

// seqErrSlot 错误结果变量的当前来源（覆盖/逃逸后失效）。
type seqErrSlot struct {
	ref       *SeqWriteErrorResult
	escaped   bool
	escapedAt token.Pos
}

// seqWriteCallID 每个**实际调用节点**一个 ID（同一源码位置被展开两次 = 两次
// 写入，不共用一个身份）；同一节点在深展开/续查中复用同一 ID。
func seqWriteCallID(req CodeSequenceRequest, node *CodeSeqNode) string {
	if req.acc == nil || node == nil {
		return ""
	}
	if id, ok := req.acc.writeIDs[node]; ok {
		return id
	}
	req.acc.writeSeq++
	id := "write-" + itoaSeq(req.acc.writeSeq)
	if req.acc.writeIDs == nil {
		req.acc.writeIDs = map[*CodeSeqNode]string{}
	}
	req.acc.writeIDs[node] = id
	return id
}

// seqWriteErrorResult 写入调用的错误槽证据（slot=1）。
func seqWriteErrorResult(req CodeSequenceRequest, node *CodeSeqNode, call *ast.CallExpr,
	c *seqPersistCtx) *SeqWriteErrorResult {
	id := seqWriteCallID(req, node)
	if id == "" {
		return nil
	}
	symbol := ""
	if node != nil && node.Evidence != nil {
		symbol = node.Evidence.SymbolID
	}
	res := &SeqWriteErrorResult{CallID: id, Repo: req.RepoAbs, Entry: req.entryID,
		Symbol: symbol, File: c.fileRel, Slot: 1}
	if call != nil && c.fset != nil {
		res.Offset = c.fset.Position(call.Lparen).Offset
	}
	return res
}

// seqErrSlotAt 该使用位置是否仍指向写入错误结果。
func (e *seqEnv) errSlotAt(obj *ast.Object, pos token.Pos) *SeqWriteErrorResult {
	if e == nil || obj == nil {
		return nil
	}
	for env := e; env != nil; env = env.parent {
		if slot, ok := env.errs[obj]; ok {
			if slot.escaped {
				if pos == token.NoPos || slot.escapedAt == token.NoPos || slot.escapedAt <= pos {
					return nil
				}
			}
			return cloneSeqWriteErrorResult(slot.ref)
		}
	}
	return nil
}

func (e *seqEnv) setErr(obj *ast.Object, ref *SeqWriteErrorResult) {
	if e == nil || obj == nil {
		return
	}
	if e.errs == nil {
		e.errs = map[*ast.Object]seqErrSlot{}
	}
	if slot, ok := e.errs[obj]; ok && slot.escaped {
		e.errs[obj] = seqErrSlot{escaped: true, escapedAt: slot.escapedAt}
		return
	}
	e.errs[obj] = seqErrSlot{ref: cloneSeqWriteErrorResult(ref)}
}

func (e *seqEnv) markErrEscaped(obj *ast.Object, pos token.Pos) {
	if e == nil || obj == nil {
		return
	}
	if e.errs == nil {
		e.errs = map[*ast.Object]seqErrSlot{}
	}
	slot := e.errs[obj]
	slot.escaped = true
	slot.escapedAt = pos
	e.errs[obj] = slot
}

func cloneSeqWriteErrorResult(r *SeqWriteErrorResult) *SeqWriteErrorResult {
	if r == nil {
		return nil
	}
	cp := *r
	return &cp
}
