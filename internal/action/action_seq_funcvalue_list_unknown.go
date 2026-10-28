package action

// T02：range 对象是声明的函数列表但值未知时的诊断（不当作空列表、不猜元素）。

import "go/ast"

// seqRangeListUnknown range 对象是声明的函数列表但值未知（形参未绑定/被未知
// 修改）→ 返回诊断原因。
func seqRangeListUnknown(a *Actions, req CodeSequenceRequest, x ast.Expr) string {
	if req.env == nil {
		return ""
	}
	var v seqValue
	ok := false
	if id, isID := x.(*ast.Ident); isID {
		v, ok = req.env.lookup(id.Obj)
	} else {
		v, ok = captureSeqCallbackLists(a, req, req.curCtx, x)
	}
	if !ok {
		return ""
	}
	if v.kind == seqValueUnknown && v.why != "" && seqListHoldsFuncValues(a, req, x, v) {
		return v.why
	}
	if v.kind == seqValueList {
		for _, e := range v.elems {
			if e.kind == seqValueUnknown && seqListTypedFuncList(a, req, x) {
				return "函数列表元素来源未知（" + e.why + "）——不按空列表/固定顺序展开"
			}
		}
	}
	return ""
}

// seqListTypedFuncList 表达式是否为声明的函数列表类型（[]func / ...func）。
func seqListTypedFuncList(a *Actions, req CodeSequenceRequest, x ast.Expr) bool {
	if seqExprFuncTypedIn(a, req, req.curCtx, x) {
		return true
	}
	id, ok := x.(*ast.Ident)
	if !ok || id.Obj == nil {
		return false
	}
	var typ ast.Expr
	switch d := id.Obj.Decl.(type) {
	case *ast.Field:
		typ = d.Type
	case *ast.ValueSpec:
		typ = d.Type
	}
	if typ == nil {
		return false
	}
	if seqEmptyFuncSliceTypeIn(a, req, req.curCtx, typ) {
		return true
	}
	if at, ok := typ.(*ast.ArrayType); ok && at.Elt != nil {
		return seqIsFuncTypeIn(a, req, req.curCtx, at.Elt, 0)
	}
	if at, ok := typ.(*ast.Ellipsis); ok && at.Elt != nil {
		return seqIsFuncTypeIn(a, req, req.curCtx, at.Elt, 0)
	}
	return false
}
