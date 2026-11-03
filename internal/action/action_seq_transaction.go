package action

// U04n：标准 SQL 事务身份、操作与独立错误槽。
//
// 事务来源是被表示的 Begin 调用身份（不是已观察到的数据库事务）：不证明
// Begin 成功、写入命中或 Commit 成功；begin/commit/rollback 各有独立错误槽。

import (
	"go/ast"
	"go/token"
)

// 事务动作。
const (
	SeqTxBegin    = "begin"
	SeqTxWrite    = "write"
	SeqTxRead     = "read"
	SeqTxCommit   = "commit"
	SeqTxRollback = "rollback"
)

// 事务边界代码。
const (
	SeqTxOriginUnproven  = "transaction_origin_unproven"
	SeqTxContextConflict = "transaction_context_conflict"
)

// 标准库事务方法身份。
var seqTxBeginMethods = map[string]bool{
	"symbol:go:database/sql:(DB).Begin":     true,
	"symbol:go:database/sql:(DB).BeginTx":   true,
	"symbol:go:database/sql:(Conn).BeginTx": true,
}

var seqTxTerminalMethods = map[string]string{
	"symbol:go:database/sql:(Tx).Commit":   SeqTxCommit,
	"symbol:go:database/sql:(Tx).Rollback": SeqTxRollback,
}

// SeqTxOrigin 事务来源（Begin 调用身份，请求内唯一）。
type SeqTxOrigin struct {
	ID     string `json:"id"`
	Repo   string `json:"repo,omitempty"`
	Entry  string `json:"entry,omitempty"`
	Symbol string `json:"symbol,omitempty"`
	File   string `json:"file,omitempty"`
	Offset int    `json:"offset,omitempty"`
	Slot   int    `json:"slot"`
}

// SeqTxOpResult 事务操作的错误返回槽（begin=1，commit/rollback=0）。
type SeqTxOpResult struct {
	CallID    string `json:"call_id"`
	Operation string `json:"operation"`
	Slot      int    `json:"slot"`
}

// SeqTransaction 事务事件（调用节点证据）。
type SeqTransaction struct {
	Origin   *SeqTxOrigin `json:"origin,omitempty"`
	Action   string       `json:"action"`
	Guard    string       `json:"guard,omitempty"`
	Exec     string       `json:"exec,omitempty"`
	Outcome  string       `json:"outcome"`
	Boundary string       `json:"boundary,omitempty"`
}

// SeqTransactionCondition 事务错误槽上的条件。
type SeqTransactionCondition struct {
	Result    *SeqTxOpResult `json:"result"`
	WhenTrue  string         `json:"when_true"`
	WhenFalse string         `json:"when_false"`
}

// seqTxSlot 事务变量槽（Begin 返回槽 0）。
type seqTxSlot struct {
	origin  *SeqTxOrigin
	escaped bool
}

// seqTxErrSlot 事务错误变量槽（操作 + 槽位）。
type seqTxErrSlot struct {
	ref     *SeqTxOpResult
	escaped bool
}

// seqTxOriginForBegin 为 Begin 调用分配事务身份（同一节点稳定）。
func seqTxOriginForBegin(req CodeSequenceRequest, node *CodeSeqNode) *SeqTxOrigin {
	if req.acc == nil || node == nil || node.Evidence == nil {
		return nil
	}
	if req.acc.txIDs == nil {
		req.acc.txIDs = map[*CodeSeqNode]string{}
	}
	if id, ok := req.acc.txIDs[node]; ok {
		return &SeqTxOrigin{ID: id}
	}
	req.acc.txSeq++
	id := "tx-" + itoaSeq(req.acc.txSeq)
	req.acc.txIDs[node] = id
	return &SeqTxOrigin{ID: id, Repo: req.RepoAbs, Entry: req.entryID,
		Symbol: node.Evidence.SymbolID, File: node.Evidence.CallFile,
		Slot: 0}
}

// seqTxReceiverOrigin 接收者变量上的事务来源。
func seqTxReceiverOrigin(req CodeSequenceRequest, e ast.Expr, pos token.Pos) *SeqTxOrigin {
	id, ok := e.(*ast.Ident)
	if !ok || id.Obj == nil || req.env == nil {
		return nil
	}
	if tx := seqTxVarOrigin(req, id); tx != nil {
		return tx
	}
	return nil
}

// seqTxVarOrigin 变量上的事务来源（直接槽或标量来源里的 transaction）。
func seqTxVarOrigin(req CodeSequenceRequest, id *ast.Ident) *SeqTxOrigin {
	if id == nil || id.Obj == nil || req.env == nil {
		return nil
	}
	for env := req.env; env != nil; env = env.parent {
		if slot, ok := env.txs[id.Obj]; ok {
			if slot.escaped || slot.origin == nil {
				return nil
			}
			cp := *slot.origin
			return &cp
		}
	}
	origin, found := req.env.scalarOriginAt(id.Obj, id.Pos())
	if found && origin != nil && origin.Kind == SeqOriginTransaction && origin.TxOrigin != nil {
		cp := *origin.TxOrigin
		return &cp
	}
	return nil
}

func (e *seqEnv) setTx(obj *ast.Object, origin *SeqTxOrigin) {
	if e == nil || obj == nil {
		return
	}
	if e.txs == nil {
		e.txs = map[*ast.Object]seqTxSlot{}
	}
	cp := *origin
	e.txs[obj] = seqTxSlot{origin: &cp}
}

func (e *seqEnv) markTxEscaped(obj *ast.Object) {
	if e == nil || obj == nil {
		return
	}
	if e.txs == nil {
		e.txs = map[*ast.Object]seqTxSlot{}
	}
	slot := e.txs[obj]
	slot.escaped = true
	slot.origin = nil
	e.txs[obj] = slot
}

func (e *seqEnv) setTxErr(obj *ast.Object, ref *SeqTxOpResult) {
	if e == nil || obj == nil {
		return
	}
	if e.txErrs == nil {
		e.txErrs = map[*ast.Object]seqTxErrSlot{}
	}
	// 事务错误与写入错误互斥（同一变量保存最近一次绑定的来源）。
	delete(e.errs, obj)
	if slot, ok := e.txErrs[obj]; ok && slot.escaped {
		e.txErrs[obj] = seqTxErrSlot{escaped: true}
		return
	}
	if ref == nil {
		e.txErrs[obj] = seqTxErrSlot{}
		return
	}
	cp := *ref
	e.txErrs[obj] = seqTxErrSlot{ref: &cp}
}

// seqTxErrRefOf 变量是否持有事务操作的错误槽。
func seqTxErrRefOf(req CodeSequenceRequest, e ast.Expr) *SeqTxOpResult {
	id, ok := e.(*ast.Ident)
	if !ok || id.Obj == nil || req.env == nil {
		return nil
	}
	for env := req.env; env != nil; env = env.parent {
		if slot, ok := env.txErrs[id.Obj]; ok {
			if slot.escaped || slot.ref == nil {
				return nil
			}
			cp := *slot.ref
			return &cp
		}
	}
	return nil
}

// copyTx 克隆事务槽（clone 用）。
func (e *seqEnv) copyTx() (map[*ast.Object]seqTxSlot, map[*ast.Object]seqTxErrSlot) {
	if e == nil {
		return map[*ast.Object]seqTxSlot{}, map[*ast.Object]seqTxErrSlot{}
	}
	txs := make(map[*ast.Object]seqTxSlot, len(e.txs))
	for k, v := range e.txs {
		txs[k] = v
	}
	errs := make(map[*ast.Object]seqTxErrSlot, len(e.txErrs))
	for k, v := range e.txErrs {
		errs[k] = v
	}
	return txs, errs
}
