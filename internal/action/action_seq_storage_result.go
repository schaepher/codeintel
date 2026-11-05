package action

// U04h：SQL 写入错误返回槽（storage.error_result）与后续条件。
//
// 只把**同一次写入调用**的错误结果绑定到变量（`_,err := DB.Exec(...)`），
// 条件只描述 error_nil/error_non_nil 的符号关系：不是运行时分支判定，
// 不改 outcome=unproven，也不证明事务提交或影响行数。

import (
	"go/ast"
	"go/token"
)

// 条件真假结果。
const (
	SeqWriteResultErrorNil    = "error_nil"
	SeqWriteResultErrorNonNil = "error_non_nil"
)

// SeqWriteErrorResult 写入错误结果身份（一次分析请求内唯一）。
type SeqWriteErrorResult struct {
	CallID string `json:"call_id"`
	Repo   string `json:"repo,omitempty"`
	Entry  string `json:"entry,omitempty"`
	Symbol string `json:"symbol,omitempty"`
	File   string `json:"file,omitempty"`
	Offset int    `json:"offset,omitempty"`
	Slot   int    `json:"slot"`
}

// SeqWriteCondition 错误变量上的条件（后续分支引用同一写入身份）。
type SeqWriteCondition struct {
	Result    *SeqWriteErrorResult `json:"result"`
	WhenTrue  string               `json:"when_true"`
	WhenFalse string               `json:"when_false"`
}

// seqErrRefOf 表达式是否为已证明的写入错误结果变量。
func seqErrRefOf(req CodeSequenceRequest, e ast.Expr) *SeqWriteErrorResult {
	id, ok := e.(*ast.Ident)
	if !ok || id.Obj == nil || req.env == nil {
		return nil
	}
	return req.env.errSlotAt(id.Obj, id.Pos())
}

// captureSeqArgErrResults 调用实参携带的写入错误结果（形参透传用）。
func captureSeqArgErrResults(req CodeSequenceRequest, call *ast.CallExpr) []*SeqWriteErrorResult {
	if call == nil {
		return nil
	}
	out := make([]*SeqWriteErrorResult, len(call.Args))
	for i, arg := range call.Args {
		out[i] = seqErrRefOf(req, arg)
	}
	return out
}

// seqBindParamErrResults 形参 ← 调用点错误结果来源。
func seqBindParamErrResults(env *seqEnv, params *ast.FieldList, refs []*SeqWriteErrorResult) {
	if env == nil || params == nil {
		return
	}
	idx := 0
	for _, field := range params.List {
		names := field.Names
		if len(names) == 0 {
			idx++
			continue
		}
		for _, name := range names {
			if name.Obj != nil && idx < len(refs) && refs[idx] != nil {
				env.setErr(name.Obj, refs[idx])
			}
			idx++
		}
	}
}

// seqRecordWriteResults 赋值/声明里的 `_,err := 写入调用` 绑定错误结果。
func seqRecordWriteResults(req CodeSequenceRequest, st ast.Stmt) {
	if req.env == nil || st == nil {
		return
	}
	switch s := st.(type) {
	case *ast.AssignStmt:
		lhs, rhs := s.Lhs, s.Rhs
		seqRecordWriteResultPair(req, lhs, rhs)
	case *ast.DeclStmt:
		gd, ok := s.Decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			return
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			lhs := make([]ast.Expr, 0, len(vs.Names))
			for _, n := range vs.Names {
				lhs = append(lhs, n)
			}
			seqRecordWriteResultPair(req, lhs, vs.Values)
		}
	}
}

// seqRecordWriteResultPair 赋值/声明的结果槽绑定（U04h）。
//   - `_, err := 写入调用(...)`：第二槽 = 该写入的错误结果；
//   - `alias := err`：别名透传（之后取址/覆盖同样失效）；
//   - 其它赋值：清掉该变量既有的错误结果来源（覆盖失效）。
func seqRecordWriteResultPair(req CodeSequenceRequest, lhs, rhs []ast.Expr) {
	if req.env == nil || len(lhs) == 0 || len(rhs) == 0 {
		return
	}
	if len(rhs) == 1 && len(lhs) == 2 {
		id, ok := lhs[1].(*ast.Ident)
		if !ok || id.Obj == nil {
			return
		}
		var ref *SeqWriteErrorResult
		var rows *SeqORMRowCountResult
		var callExpr *ast.CallExpr
		if call, ok2 := rhs[0].(*ast.CallExpr); ok2 {
			callExpr = call
			ref = req.writeCalls[call]
			rows = req.rowCalls[call]
			// U04m：`result, err := DB.Exec(...)` 的槽 0 = sql.Result（写入身份）。
			seqBindSQLResultOrigin(req, call, lhs)
			// U04d：`rows, err := DB.Query(...)` 的槽 0 = 读取结果身份。
			seqBindReadResultOrigin(req, call, lhs)
			// B05：`id, err := result.LastInsertId()` 的槽 0 = 生成键。
			seqBindGeneratedKeyOrigin(req, call, lhs)
			// U04m：`rows, countErr := result.RowsAffected()` 的槽 0 = 影响行数。
			if rc := seqRowsAffectedRowCount(req, call); rc != nil {
				rows = rc
			}
		}
		req.env.setErr(id.Obj, ref)
		// U04j：第 0 槽 = 影响行数（XORM 写入签名已证明）。
		if r0, ok3 := lhs[0].(*ast.Ident); ok3 && r0.Obj != nil && rows != nil {
			req.env.setRows(r0.Obj, rows)
			req.env.setScalar(r0.Obj, &SeqStorageOrigin{Kind: SeqOriginWriteRowCount,
				ScalarType: "int64", RowCount: cloneSeqRowCount(rows)})
		}
		// U04k：返回槽来源来自实际展开（或 depth=1 的续查结果）。
		if callExpr != nil {
			seqBindTxResults(req, seqCallNodeOf(req, callExpr), callExpr, lhs)
			seqBindReturnOrigins(req, callExpr, lhs, []int{0, 1})
		}
		return
	}
	if len(lhs) != len(rhs) {
		return
	}
	for i, lhsExpr := range lhs {
		id, ok := lhsExpr.(*ast.Ident)
		if !ok || id.Obj == nil {
			continue
		}
		if origin := seqArgReturnOrigin(req, rhs[i]); origin != nil {
			if seqBindOriginToVar(req.env, id.Obj, origin) {
				continue
			}
			if origin.Kind != SeqOriginWriteError && origin.Kind != SeqOriginWriteRowCount &&
				origin.Kind != SeqOriginTxError && origin.Kind != SeqOriginTransaction &&
				origin.Kind != SeqOriginNil {
				req.env.setScalar(id.Obj, origin)
				continue
			}
		}
		if call, ok := rhs[i].(*ast.CallExpr); ok {
			// U04n：`err = tx.Commit()`（单槽）→ 事务操作错误槽。
			seqBindTxResults(req, seqCallNodeOf(req, call), call, singleLHSAt(lhs, i))
			node := seqCallNodeOf(req, call)
			if node != nil && len(node.Nodes) == 0 {
				// U04k：depth=1 未展开——续查完成后回填（error/row_count/标量）。
				seqRegisterPendingVar(req, call, id.Obj, 0)
			}
		}
		ref := seqErrRefOf(req, rhs[i])
		if ref != nil {
			req.env.setErr(id.Obj, ref)
			continue
		}
		if req.env.hasErrSlot(id.Obj) {
			req.env.setErr(id.Obj, nil)
		}
		if req.env.hasRowsSlot(id.Obj) {
			req.env.setRows(id.Obj, nil)
		}
	}
}

// singleLHSAt 单值赋值语义下的 LHS 槽列表（仅当 LHS 只有一个占位时）。
func singleLHSAt(lhs []ast.Expr, i int) []ast.Expr {
	if len(lhs) != 1 || i != 0 {
		return nil
	}
	return lhs
}

// seqArgReturnOrigin 单返回调用/变量引用的返回槽来源（深/浅展开统一）。
func seqArgReturnOrigin(req CodeSequenceRequest, e ast.Expr) *SeqStorageOrigin {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return nil
	}
	return seqNodeResultOriginAt(req, call, 0)
}

// seqBindReturnOrigins 多槽赋值：按槽来源绑定（error/row_count）；来源未知时
// 登记 depth=1 的延迟绑定，续查完成后回填。
func seqBindReturnOrigins(req CodeSequenceRequest, call *ast.CallExpr, lhs []ast.Expr, slots []int) {
	for _, slot := range slots {
		if slot >= len(lhs) {
			continue
		}
		id, ok := lhs[slot].(*ast.Ident)
		if !ok || id.Obj == nil || id.Name == "_" {
			continue
		}
		origin := seqNodeResultOriginAt(req, call, slot)
		if origin == nil {
			if seqCallNodeOf(req, call) != nil && len(seqCallNodeOf(req, call).Nodes) == 0 {
				// depth=1：调用内部尚未展开，续查后回填。
				seqRegisterPendingVar(req, call, id.Obj, slot)
			}
			continue
		}
		if seqBindOriginToVar(req.env, id.Obj, origin) {
			continue
		}
		// 普通标量来源（如构造器返回的表单键）也沿返回槽传播。
		if origin.Kind == SeqOriginParameter || origin.Kind == SeqOriginLiteral ||
			origin.Kind == SeqOriginConcat {
			req.env.setScalar(id.Obj, origin)
		}
	}
}

// hasErrSlot 该变量是否曾绑定写入错误结果（避免为所有变量建空槽）。
func (e *seqEnv) hasErrSlot(obj *ast.Object) bool {
	for env := e; env != nil; env = env.parent {
		if _, ok := env.errs[obj]; ok {
			return true
		}
	}
	return false
}
