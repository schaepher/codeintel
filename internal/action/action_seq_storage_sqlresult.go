package action

// U04m：SQL Result.RowsAffected → 准确的原始写入（sql_result 来源）。
//
// 只有**准确的** `database/sql:(Result).RowsAffected` 调用且接收者是已证明的
// Exec 返回槽 0 时，才把它的槽 0 标成该写入的影响行数来源；计数读取错误
// （RowsAffected 自己的 error 槽）与事务提交分别保持边界。

import (
	"go/ast"

	"github.com/schaepher/codeintel/internal/domain"
)

// 标准库方法身份。
const seqSQLRowsAffected = "symbol:go:database/sql:(Result).RowsAffected"

// seqIsSQLWriteCall 该 AST 调用是否是已证明的 SQL 写入（DB/Tx Exec）。
func seqIsSQLWriteCall(req CodeSequenceRequest, call *ast.CallExpr) *SeqWriteErrorResult {
	if req.acc == nil || call == nil {
		return nil
	}
	node := seqCallNodeOf(req, call)
	if node == nil || node.Evidence == nil || node.Evidence.Storage == nil {
		return nil
	}
	return node.Evidence.Storage.ErrorResult
}

// seqBindSQLResultOrigin 绑定 `result, err := DB.Exec(...)` 的槽 0 来源。
func seqBindSQLResultOrigin(req CodeSequenceRequest, call *ast.CallExpr, lhs []ast.Expr) {
	ref := seqIsSQLWriteCall(req, call)
	if ref == nil || len(lhs) == 0 {
		return
	}
	id, ok := lhs[0].(*ast.Ident)
	if !ok || id.Obj == nil || id.Name == "_" {
		return
	}
	req.env.setScalar(id.Obj, &SeqStorageOrigin{Kind: SeqOriginSQLResult, ScalarType: "sql.Result",
		SQLResult: &SeqSQLResult{Write: cloneSeqWriteErrorResult(ref), Slot: 0}})
}

// seqRowsAffectedRowCount `x.RowsAffected()` → 行数槽（接收者必须是 sql_result）。
func seqRowsAffectedRowCount(req CodeSequenceRequest, call *ast.CallExpr) *SeqORMRowCountResult {
	if req.acc == nil || call == nil || req.env == nil {
		return nil
	}
	node := seqCallNodeOf(req, call)
	if node == nil || node.Evidence == nil || node.Evidence.SymbolID != seqSQLRowsAffected {
		return nil
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok || id.Obj == nil {
		return nil
	}
	origin, found := req.env.scalarOriginAt(id.Obj, id.Pos())
	if !found || origin == nil || origin.Kind != SeqOriginSQLResult || origin.SQLResult == nil {
		return nil
	}
	access := &SeqRowCountAccess{Symbol: seqSQLRowsAffected, File: node.Evidence.CallFile,
		Offset: node.Evidence.CallLine, ErrorSlot: 1, ErrorOutcome: "unproven"}
	if req.acc.fset != nil && call.Lparen.IsValid() {
		access.Offset = req.acc.fset.Position(call.Lparen).Offset
	}
	return &SeqORMRowCountResult{Write: cloneSeqWriteErrorResult(origin.SQLResult.Write),
		Slot: 0, Access: access}
}

// seqSQLResultOf 变量是否持有某次 Exec 的 sql.Result（准确写入身份）。
func seqSQLResultOf(req CodeSequenceRequest, e ast.Expr) *SeqSQLResult {
	id, ok := e.(*ast.Ident)
	if !ok || id.Obj == nil || req.env == nil {
		return nil
	}
	origin, found := req.env.scalarOriginAt(id.Obj, id.Pos())
	if !found || origin == nil || origin.Kind != SeqOriginSQLResult {
		return nil
	}
	return origin.SQLResult
}

// seqMethodNameOfCall 调用的方法名（selector 形态）。
func seqMethodNameOfCall(call *ast.CallExpr) string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	return sel.Sel.Name
}

// seqSymbolOfCall 本次请求里该调用的准确 canonical 目标（无则空）。
func seqSymbolOfCall(req CodeSequenceRequest, call *ast.CallExpr) string {
	if req.acc == nil || call == nil {
		return ""
	}
	node := req.acc.callNodes[call]
	if node == nil || node.Evidence == nil {
		return ""
	}
	return node.Evidence.SymbolID
}

var _ = domain.CanonicalID("")

// selectorReceiver `x.M()` → x（非 selector 调用返回 nil）。
func selectorReceiver(call *ast.CallExpr) ast.Expr {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	return sel.X
}
