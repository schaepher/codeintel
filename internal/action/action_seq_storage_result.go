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
		if call, ok2 := rhs[0].(*ast.CallExpr); ok2 {
			ref = req.writeCalls[call]
		}
		req.env.setErr(id.Obj, ref)
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
		ref := seqErrRefOf(req, rhs[i])
		if ref != nil {
			req.env.setErr(id.Obj, ref)
			continue
		}
		if req.env.hasErrSlot(id.Obj) {
			req.env.setErr(id.Obj, nil)
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
