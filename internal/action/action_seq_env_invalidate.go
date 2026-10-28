package action

// todo24 §11.5：回调槽的**失效传播**——取址实参（`Replace(&f)`）与 range 赋值
// 目标（`for _, f = range others`）都会让已绑定的函数值来源失效；循环内/之后
// 不得沿用旧绑定（保守降级为未知，而不是猜）。

import (
	"go/ast"
	"go/token"
)

// seqInvalidateAddrTaken 实参里出现 `&f`（f 是已绑定的函数值）→ f 可能被
// 调用改写：把该槽降级为未知（todo24 §11.5：取址逃逸不得沿用旧来源）。
func seqInvalidateAddrTaken(req CodeSequenceRequest, call *ast.CallExpr) {
	if req.env == nil || call == nil {
		return
	}
	for _, arg := range call.Args {
		un, ok := arg.(*ast.UnaryExpr)
		if !ok || un.Op != token.AND {
			continue
		}
		id, ok := un.X.(*ast.Ident)
		if !ok || id.Obj == nil {
			continue
		}
		v, ok := req.env.lookup(id.Obj)
		if !ok {
			continue
		}
		switch v.kind {
		case seqValueLit, seqValueNamed, seqValueMulti, seqValueList:
			req.env.set(id.Obj, unknownSeqValue("函数值被取址传入调用（可能被改写）"))
		}
	}
}

// seqInvalidateRangeFuncSlots range 的赋值目标（`for _, f = range xs`）是已绑定
// 函数值时：循环元素来源未知/非函数列表 → 循环内不得回退成循环前的 f
// （todo24 §11.5）。
func seqInvalidateRangeFuncSlots(req CodeSequenceRequest, s *ast.RangeStmt) {
	if req.env == nil || s == nil {
		return
	}
	for _, target := range []ast.Expr{s.Key, s.Value} {
		id, ok := target.(*ast.Ident)
		if !ok || id.Obj == nil {
			continue
		}
		v, ok := req.env.lookup(id.Obj)
		if !ok {
			continue
		}
		switch v.kind {
		case seqValueLit, seqValueNamed, seqValueMulti, seqValueList:
			req.env.set(id.Obj, unknownSeqValue("range 赋值的函数槽（循环元素来源未知）"))
		}
	}
}
