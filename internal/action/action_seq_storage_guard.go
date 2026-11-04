package action

// U04k：错误守卫下的 nil 返回等价（未受守卫的 nil 路径保持未知）。

import (
	"go/ast"
	"go/token"
)

// seqRecordErrNilGuard 若 `if err != nil { ... return err }` 的 then 分支在
// 所有路径返回该错误，则后续语句处于 err==nil 路径——登记为 nil return 的
// 守卫身份（U04k）。
func seqRecordErrNilGuard(req CodeSequenceRequest, st *ast.IfStmt) {
	if st == nil || req.env == nil || st.Body == nil {
		return
	}
	ref := seqErrRefOf(req, seqErrOperandOf(st.Cond))
	if ref == nil {
		return
	}
	// then 分支必须以 return 结束（guard 子句）；允许嵌套块。
	if !seqBlockTerminatesWithReturn(st.Body) {
		return
	}
	returnsErr := false
	ast.Inspect(st.Body, func(n ast.Node) bool {
		ret, ok := n.(*ast.ReturnStmt)
		if !ok || len(ret.Results) != 1 {
			return true
		}
		id, ok := ret.Results[0].(*ast.Ident)
		if !ok || req.env == nil {
			return true
		}
		if sameSeqWriteError(req.env.errSlotAt(id.Obj, id.Pos()), ref) {
			returnsErr = true
			return false
		}
		return true
	})
	if !returnsErr {
		return
	}
	if req.acc == nil {
		return
	}
	if req.acc.errNilGuards == nil {
		req.acc.errNilGuards = map[string]map[*SeqWriteErrorResult]bool{}
	}
	guards := req.acc.errNilGuards[req.curSym]
	if guards == nil {
		guards = map[*SeqWriteErrorResult]bool{}
		req.acc.errNilGuards[req.curSym] = guards
	}
	guards[ref] = true
}

// seqErrOperandOf 条件里的错误变量操作数（err != nil / nil != err）。
func seqErrOperandOf(cond ast.Expr) ast.Expr {
	expr, _ := seqUnwrapNegation(cond)
	cmp, ok := expr.(*ast.BinaryExpr)
	if !ok || (cmp.Op != token.EQL && cmp.Op != token.NEQ) {
		return nil
	}
	if id, ok := cmp.X.(*ast.Ident); ok && id.Name == "nil" && id.Obj == nil {
		return cmp.Y
	}
	if id, ok := cmp.Y.(*ast.Ident); ok && id.Name == "nil" && id.Obj == nil {
		return cmp.X
	}
	return nil
}

// seqBlockTerminatesWithReturn 块的最后一个语句是 return（guard 子句）。
func seqBlockTerminatesWithReturn(body *ast.BlockStmt) bool {
	if body == nil || len(body.List) == 0 {
		return false
	}
	_, ok := body.List[len(body.List)-1].(*ast.ReturnStmt)
	return ok
}
