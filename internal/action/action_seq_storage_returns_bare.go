package action

// U04k：具名裸 return 的槽来源（从 action_seq_storage_returns.go 拆出）。

import (
	"go/token"
)

// seqBareReturnOrigins 具名裸 return：按结果对象取来源（守卫见 U04k）。
func seqBareReturnOrigins(req CodeSequenceRequest) []*SeqStorageOrigin {
	if len(req.resultObjs) == 0 {
		return nil
	}
	out := make([]*SeqStorageOrigin, 0, len(req.resultObjs))
	for _, obj := range req.resultObjs {
		if obj == nil || req.env == nil {
			out = append(out, nil)
			continue
		}
		// B06e.4：命名返回值 `func open()(tx *sql.Tx, err error){...;return}`
		// 的事务来源在 env.txs 槽里（不是写入错误/标量来源）。
		if tx := seqTxObjOrigin(req, obj); tx != nil {
			out = append(out, &SeqStorageOrigin{Kind: SeqOriginTransaction, TxOrigin: tx})
			continue
		}
		if ref := req.env.errSlotAt(obj, token.NoPos); ref != nil {
			out = append(out, &SeqStorageOrigin{Kind: SeqOriginWriteError, WriteError: ref})
			continue
		}
		if rc := req.env.rowsResultOf(obj, token.NoPos); rc != nil {
			out = append(out, &SeqStorageOrigin{Kind: SeqOriginWriteRowCount,
				ScalarType: "int64", RowCount: rc})
			continue
		}
		if origin, ok := req.env.scalarOriginAt(obj, token.NoPos); ok && origin != nil {
			out = append(out, cloneSeqScalarOrigin(origin))
			continue
		}
		out = append(out, nil)
	}
	return out
}
