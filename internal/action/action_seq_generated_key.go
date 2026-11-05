package action

// B05：数据库生成键来源（`Result.LastInsertId`）。
//
// 只接受**准确的** database/sql `(Result).LastInsertId`，接收者必须是某次
// Exec 的 sql.Result（原写入 call_id）；Exec 成功与取键成功是两项独立前提。

import (
	"go/ast"
)

const seqSQLLastInsertID = "symbol:go:database/sql:(Result).LastInsertId"

// SeqGeneratedKeyAccess 取键调用身份。
type SeqGeneratedKeyAccess struct {
	Symbol string `json:"symbol,omitempty"`
	File   string `json:"file,omitempty"`
	Offset int    `json:"offset,omitempty"`
}

// SeqGeneratedKey 生成键来源（关联原始写入）。
type SeqGeneratedKey struct {
	CallID   string                 `json:"call_id,omitempty"`
	Slot     int                    `json:"slot,omitempty"`
	Symbol   string                 `json:"symbol,omitempty"`
	Guard    string                 `json:"guard,omitempty"`
	Access   *SeqGeneratedKeyAccess `json:"access,omitempty"`
	Premises []string               `json:"success_conditions,omitempty"`
	Outcome  string                 `json:"outcome"`
	Unproven bool                   `json:"unproven,omitempty"`
}

// seqGeneratedKeyOriginAt 已证明的 LastInsertId 调用 → 生成键来源。
func seqGeneratedKeyOriginAt(req CodeSequenceRequest, call *ast.CallExpr) *SeqStorageOrigin {
	if call == nil || req.env == nil {
		return nil
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "LastInsertId" || len(call.Args) != 0 {
		return nil
	}
	node := seqCallNodeOf(req, call)
	if node == nil || node.Evidence == nil {
		return nil
	}
	if node.Evidence.SymbolID != "" && node.Evidence.SymbolID != seqSQLLastInsertID {
		// 解析到其它方法（同名自定义）——不借用标准库身份。
		return nil
	}
	id, ok := runtimeUnparen(sel.X).(*ast.Ident)
	if !ok {
		return nil
	}
	res := seqSQLResultOf(req, id)
	if res == nil || res.Write == nil {
		return nil
	}
	access := &SeqGeneratedKeyAccess{Symbol: seqSQLLastInsertID,
		File: node.Evidence.CallFile, Offset: node.Evidence.CallLine}
	if req.acc != nil && req.acc.fset != nil && call.Lparen.IsValid() {
		access.Offset = req.acc.fset.Position(call.Lparen).Offset
	}
	return &SeqStorageOrigin{Kind: SeqOriginGeneratedKey, ScalarType: "int64",
		GeneratedKey: &SeqGeneratedKey{CallID: res.Write.CallID, Slot: 0,
			Symbol: res.Write.Symbol, Guard: req.branchCond, Access: access,
			Premises: []string{"exec_success", "last_insert_id_success"},
			Outcome:  "unproven", Unproven: true}}
}

// seqBindGeneratedKeyOrigin 多槽赋值 `id, err := r.LastInsertId()`：槽 0 生成键。
func seqBindGeneratedKeyOrigin(req CodeSequenceRequest, call *ast.CallExpr, lhs []ast.Expr) {
	if req.env == nil || call == nil || len(lhs) == 0 {
		return
	}
	id, ok := lhs[0].(*ast.Ident)
	if !ok || id.Obj == nil || id.Name == "_" {
		return
	}
	if o := seqGeneratedKeyOriginAt(req, call); o != nil {
		req.env.setScalar(id.Obj, o)
	}
}
