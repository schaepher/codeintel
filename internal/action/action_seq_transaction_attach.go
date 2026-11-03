package action

// U04n：事务事件证据挂载与返回槽绑定。

import "go/ast"

// seqAttachTransaction 事务事件证据（Begin/写入/读取/Commit/Rollback）。
func seqAttachTransaction(req CodeSequenceRequest, node *CodeSeqNode, call *ast.CallExpr,
	c *seqPersistCtx) {
	if node == nil || node.Evidence == nil || node.Evidence.SymbolID == "" {
		return
	}
	id := node.Evidence.SymbolID
	if seqTxBeginMethods[id] {
		origin := seqTxOriginForBegin(req, node)
		if origin == nil {
			return
		}
		origin.Symbol, origin.File = id, node.Evidence.CallFile
		if c != nil && c.fset != nil && call != nil {
			origin.Offset = c.fset.Position(call.Lparen).Offset
		}
		node.Evidence.Transaction = &SeqTransaction{Origin: origin, Action: SeqTxBegin,
			Outcome: "unproven", Exec: node.Exec}
		return
	}
	if action, ok := seqTxTerminalMethods[id]; ok {
		origin := seqTxReceiverOrigin(req, selectorReceiver(call), call.Pos())
		tr := &SeqTransaction{Action: action, Outcome: "unproven", Exec: node.Exec}
		if origin != nil {
			tr.Origin = origin
		} else {
			tr.Boundary = SeqTxOriginUnproven
		}
		node.Evidence.Transaction = tr
		return
	}
	// 事务上的写入/读取：Tx 方法 + 已证明的接收者。
	if action := seqTxMethodAction(id); action != "" {
		origin := seqTxReceiverOrigin(req, selectorReceiver(call), call.Pos())
		tr := &SeqTransaction{Action: action, Outcome: "unproven", Exec: node.Exec}
		if origin != nil {
			tr.Origin = origin
		} else {
			tr.Boundary = SeqTxOriginUnproven
		}
		node.Evidence.Transaction = tr
	}
}

// seqTxMethodAction Tx 方法 → write/read 动作。
func seqTxMethodAction(id string) string {
	switch id {
	case "symbol:go:database/sql:(Tx).Exec", "symbol:go:database/sql:(Tx).ExecContext":
		return SeqTxWrite
	case "symbol:go:database/sql:(Tx).Query", "symbol:go:database/sql:(Tx).QueryContext",
		"symbol:go:database/sql:(Tx).QueryRow", "symbol:go:database/sql:(Tx).QueryRowContext":
		return SeqTxRead
	}
	return ""
}

// seqBindTxResults 绑定 Begin/Commit/Rollback 的返回槽（事务 + 独立错误槽）。
func seqBindTxResults(req CodeSequenceRequest, node *CodeSeqNode, call *ast.CallExpr, lhs []ast.Expr) {
	if req.env == nil || node == nil || node.Evidence == nil || call == nil {
		return
	}
	id := node.Evidence.SymbolID
	var op string
	slot := -1
	switch {
	case seqTxBeginMethods[id]:
		op, slot = SeqTxBegin, 1
	case id == "symbol:go:database/sql:(Tx).Commit":
		op, slot = SeqTxCommit, 0
	case id == "symbol:go:database/sql:(Tx).Rollback":
		op, slot = SeqTxRollback, 0
	default:
		return
	}
	if op == SeqTxBegin && len(lhs) >= 1 {
		if txID, ok := lhs[0].(*ast.Ident); ok && txID.Obj != nil && txID.Name != "_" {
			if origin := seqTxOriginForBegin(req, node); origin != nil {
				origin.Symbol = id
				origin.File = node.Evidence.CallFile
				req.env.setTx(txID.Obj, origin)
			}
		}
	}
	if slot >= 0 && slot < len(lhs) {
		if errID, ok := lhs[slot].(*ast.Ident); ok && errID.Obj != nil && errID.Name != "_" {
			ref := &SeqTxOpResult{CallID: seqWriteCallID(req, node), Operation: op, Slot: slot}
			req.env.setTxErr(errID.Obj, ref)
		}
	}
}
