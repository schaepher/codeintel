package action

// U04k/U04l：从实际调用展开的 return 语句采集返回槽来源（value_origins →
// 调用节点 result_origins），供调用方赋值/条件使用。
//
// 不重放函数体：只读已展开子树里**属于该被调函数**的 return 节点（嵌套调用
// 的 return 不属于外层）。

import (
	"go/ast"
	"go/token"
)

// SeqOriginNil 字面 nil（U04l：`return nil` 的原始证据保留）。
const SeqOriginNil = "nil"

// SeqOriginWriteError 返回槽携带写入错误身份（U04l）。
const SeqOriginWriteError = "write_error"

// seqReturnOrigins 计算 return 语句各结果槽的来源（按书写顺序）。
func seqReturnOrigins(req CodeSequenceRequest, results []ast.Expr,
	calls []*CodeSeqNode) []*SeqStorageOrigin {
	if len(results) == 1 {
		// `return F()`：按 F 的声明结果数展开（不把一个 tuple 当一个槽）。
		if call, ok := results[0].(*ast.CallExpr); ok {
			if node := seqCallNodeOf(req, call); node != nil {
				if origins := seqNodeResultOrigins(node); len(origins) > 0 {
					return origins
				}
				// 外部写入调用（不入展开树）：按证据直接给出槽来源。
				if origins := seqNodeOriginsFromEvidence(node); len(origins) > 0 {
					return origins
				}
			}
		}
	}
	if len(results) == 0 {
		return seqBareReturnOrigins(req)
	}
	out := make([]*SeqStorageOrigin, 0, len(results))
	for _, r := range results {
		out = append(out, seqReturnOriginOf(req, r))
	}
	return out
}

// seqBareReturnOrigins 具名裸 return：按结果对象取来源。
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

// seqReturnOriginOf 单个返回表达式的来源。
func seqReturnOriginOf(req CodeSequenceRequest, e ast.Expr) *SeqStorageOrigin {
	if e == nil {
		return nil
	}
	if id, ok := e.(*ast.Ident); ok {
		if id.Name == "nil" && id.Obj == nil {
			return &SeqStorageOrigin{Kind: SeqOriginNil}
		}
		if id.Obj != nil && req.env != nil {
			if tx := seqTxVarOrigin(req, id); tx != nil {
				return &SeqStorageOrigin{Kind: SeqOriginTransaction, TxOrigin: tx}
			}
			if txErr := seqTxErrRefOf(req, id); txErr != nil {
				return &SeqStorageOrigin{Kind: SeqOriginTxError, TxError: txErr}
			}
			if ref := req.env.errSlotAt(id.Obj, id.Pos()); ref != nil {
				return &SeqStorageOrigin{Kind: SeqOriginWriteError, WriteError: ref}
			}
			if rc := req.env.rowsResultOf(id.Obj, id.Pos()); rc != nil {
				return &SeqStorageOrigin{Kind: SeqOriginWriteRowCount, ScalarType: "int64", RowCount: rc}
			}
		}
	}
	return seqScalarOriginOf(req, e)
}

// seqNodeOriginsFromEvidence 不展开的写入调用：按证据给出结果槽来源。
func seqNodeOriginsFromEvidence(node *CodeSeqNode) []*SeqStorageOrigin {
	if node == nil || node.Evidence == nil {
		return nil
	}
	ev := node.Evidence
	if orm := ev.ORM; orm != nil && orm.RowCountResult != nil && orm.ErrorResult != nil {
		return []*SeqStorageOrigin{
			{Kind: SeqOriginWriteRowCount, ScalarType: "int64", RowCount: cloneSeqRowCount(orm.RowCountResult)},
			{Kind: SeqOriginWriteError, WriteError: cloneSeqWriteErrorResult(&orm.ErrorResult.SeqWriteErrorResult)},
		}
	}
	if st := ev.Storage; st != nil && st.ErrorResult != nil {
		return []*SeqStorageOrigin{nil, {Kind: SeqOriginWriteError, WriteError: st.ErrorResult}}
	}
	if tx := ev.Transaction; tx != nil && tx.Origin != nil {
		// Begin 返回 (tx, error)：槽 0 事务、槽 1 事务错误（begin=1）。
		return []*SeqStorageOrigin{
			{Kind: SeqOriginTransaction, TxOrigin: tx.Origin},
			{Kind: SeqOriginTxError, TxError: &SeqTxOpResult{CallID: "", Operation: SeqTxBegin, Slot: 1}},
		}
	}
	return nil
}

// seqCallNodeOf 本次请求里某 AST 调用对应的最新调用节点。
func seqCallNodeOf(req CodeSequenceRequest, call *ast.CallExpr) *CodeSeqNode {
	if req.acc == nil || call == nil {
		return nil
	}
	return req.acc.callNodes[call]
}

// seqNodeResultOrigins 调用节点的返回槽来源共识：只取该被调函数自己的
// return 节点（进入嵌套调用子树即停），同一槽全部路径一致才保留。
func seqNodeResultOrigins(node *CodeSeqNode) []*SeqStorageOrigin {
	if node == nil || len(node.Nodes) == 0 {
		return nil
	}
	var rets [][]*SeqStorageOrigin
	var walk func(nodes []*CodeSeqNode)
	walk = func(nodes []*CodeSeqNode) {
		for _, n := range nodes {
			if n == nil {
				continue
			}
			if n.Kind == "call" {
				// 嵌套调用的 return 属于被调者，不是本函数的返回路径。
				continue
			}
			if n.Kind == "return" {
				rets = append(rets, n.ValueOrigins)
				continue
			}
			walk(n.Nodes)
			walk(n.Else)
		}
	}
	walk(node.Nodes)
	if len(rets) == 0 {
		return nil
	}
	slots := 0
	for _, r := range rets {
		if len(r) > slots {
			slots = len(r)
		}
	}
	out := make([]*SeqStorageOrigin, slots)
	for i := 0; i < slots; i++ {
		out[i] = seqConsensusOrigin(rets, i)
	}
	return out
}

// seqConsensusOrigin 第 idx 槽的共识来源：nil 路径（错误槽）不参与；
// 其余来源必须一致。
func seqConsensusOrigin(paths [][]*SeqStorageOrigin, idx int) *SeqStorageOrigin {
	// U04l：`return nil` 是成功路径，不否定写入错误/行数来源；全部为 nil
	// 时保留字面 nil 证据。
	var first *SeqStorageOrigin
	sawNil := false
	for _, path := range paths {
		if idx >= len(path) {
			return nil
		}
		o := path[idx]
		if o == nil {
			return nil
		}
		if o.Kind == SeqOriginNil {
			sawNil = true
			continue
		}
		if first == nil {
			first = o
			continue
		}
		if !sameSeqScalarOrigin(first, o) {
			return nil
		}
	}
	if first == nil {
		if sawNil {
			return &SeqStorageOrigin{Kind: SeqOriginNil}
		}
		return nil
	}
	return cloneSeqScalarOrigin(first)
}

// seqNodeResultOriginAt 调用节点第 slot 个返回来源（无则 nil）。
func seqNodeResultOriginAt(req CodeSequenceRequest, call *ast.CallExpr, slot int) *SeqStorageOrigin {
	node := seqCallNodeOf(req, call)
	if node == nil {
		return nil
	}
	origins := seqNodeResultOrigins(node)
	if slot < 0 || slot >= len(origins) {
		return nil
	}
	return origins[slot]
}

// seqBindOriginToVar 把调用结果的某个槽来源绑定到调用方变量。
func seqBindOriginToVar(env *seqEnv, obj *ast.Object, origin *SeqStorageOrigin) bool {
	if env == nil || obj == nil || origin == nil {
		return false
	}
	switch origin.Kind {
	case SeqOriginTransaction:
		if origin.TxOrigin != nil {
			env.setTx(obj, origin.TxOrigin)
			env.setScalar(obj, cloneSeqScalarOrigin(origin))
			return true
		}
	case SeqOriginTxError:
		if origin.TxError != nil {
			env.setTxErr(obj, origin.TxError)
			return true
		}
	case SeqOriginWriteError:
		if origin.WriteError != nil {
			env.setErr(obj, origin.WriteError)
			return true
		}
	case SeqOriginWriteRowCount:
		if origin.RowCount != nil {
			env.setRows(obj, origin.RowCount)
			env.setScalar(obj, cloneSeqScalarOrigin(origin))
			return true
		}
	}
	return false
}

// seqReturnCtx 一个 return 节点的来源重算上下文（U04k todo29）。
type seqReturnCtx struct {
	req     CodeSequenceRequest
	results []ast.Expr
}

// seqRecordReturnCtx 记录 return 表达式，供续查后重算槽来源。
func seqRecordReturnCtx(req CodeSequenceRequest, node *CodeSeqNode, results []ast.Expr) {
	if req.acc == nil || node == nil {
		return
	}
	if req.acc.returnCtx == nil {
		req.acc.returnCtx = map[*CodeSeqNode]seqReturnCtx{}
	}
	req.acc.returnCtx[node] = seqReturnCtx{req: req, results: append([]ast.Expr(nil), results...)}
}

// seqRefreshReturnOrigins 续查完成后**后序**重算全部 return 槽来源：内层
// 包装函数的 return 可能首轮未展开（depth 前沿），固化会把外层 `return F()`
// 的来源留空，导致调用方丢失错误/行数条件。
func seqRefreshReturnOrigins(req CodeSequenceRequest, root *CodeSeqNode) {
	if root == nil {
		return
	}
	for _, c := range root.Nodes {
		seqRefreshReturnOrigins(req, c)
	}
	for _, c := range root.Else {
		seqRefreshReturnOrigins(req, c)
	}
	if req.acc == nil {
		return
	}
	ctx, ok := req.acc.returnCtx[root]
	if !ok {
		return
	}
	root.ValueOrigins = seqReturnOrigins(ctx.req, ctx.results, nil)
}
