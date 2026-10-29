package action

// todo22 §2（第一增量）：函数值调用的展开。
//
// 触发展开的三种情形（都被证明"在这个调用点执行"）：
//   1. IIFE：`func(){…}()`——调用处直接是字面量（literal）
//   2. 函数变量/形参：`f()` / `process(inner)`——按调用点的到达定义取值
//      （var），形参值来自调用实参绑定（arg → callback）
//   3. 局部变量指向包级函数：`f := Second; f()`——from var
//
// 不能证明的三种情形都不展开内部执行，但**保留节点并给出原因**：
//   未绑定/被未知调用改写（unknown）、条件/循环下多值未消歧（multi）、
//   参数在外部调用里逃逸（调用节点的 Stop.Detail 标注）。

import (
	"go/ast"
)

// seqCallFuncValue 判断被调者本身是否是函数值（字面量/变量/形参）。
// ok=false 表示按索引调用边/接口派发等既有路径处理（包级函数的直接调用）。
func seqCallFuncValue(a *Actions, req CodeSequenceRequest, call *ast.CallExpr) (seqValue, bool) {
	switch f := call.Fun.(type) {
	case *ast.FuncLit:
		v := captureSeqClosureValue(a, req, req.curCtx, f)
		v.origin = seqOriginLiteral
		return v, true
	case *ast.Ident:
		if req.env != nil {
			if v, ok := req.env.lookup(f.Obj); ok {
				return v, true
			}
		}
	}
	return seqValue{}, false
}

// seqFillFuncValueCall 把函数值调用的展开结果写进调用节点（node 已含
// label/actor/line）。

// 展开证据 reason（origin → 可读依据）。
const (
	seqReasonLiteral = "iife"               // 调用处直接是字面量
	seqReasonArg     = "callback-binding"   // 形参值来自调用实参（已绑定）
	seqReasonVar     = "func-value-var"     // 局部变量/包级函数的到达定义
	seqReasonUnknown = "func-value-unknown" // 调用点无法确定回调值
	seqReasonMulti   = "func-value-multi"   // 多值未消歧
	seqReasonList    = "func-list"          // 函数列表逐项执行
)

func seqReasonForOrigin(origin string) string {
	switch origin {
	case seqOriginLiteral:
		return seqReasonLiteral
	case seqOriginArg:
		return seqReasonArg
	}
	return seqReasonVar
}

// seqExpandSingleValue 展开**一个**函数值：字面量走内联遍历，具名函数走
// codeSeqForSymbol（两者都把本次调用的实参绑定到被展开函数的形参）。

// seqFuncArgEscape 判断调用是否让函数值参数逃逸（传给未解析/外部目标）：
// 逃逸后无法确定是否、何时被调用——在调用节点标注原因，不展开内部执行。
func seqFuncArgEscape(a *Actions, req CodeSequenceRequest, call *ast.CallExpr) string {
	if req.env == nil || call == nil {
		return ""
	}
	for _, arg := range call.Args {
		v := captureSeqClosureValue(a, req, req.curCtx, arg)
		switch v.kind {
		case seqValueLit, seqValueNamed, seqValueMulti:
			return "函数值参数在未解析调用中逃逸（无法确定是否/何时被调用）: " + v.label()
		}
		// 值未知但**声明为**函数类型：同样是函数值逃逸（不能因为不知道是
		// 哪个回调就当作普通参数）。
		if seqExprFuncTypedIn(a, req, req.curCtx, arg) {
			name := seqExprText(req.curCtx.fset, req.curCtx.src, arg)
			return "函数值参数 " + name + " 在未解析调用中逃逸（无法确定是否/何时被调用）"
		}
	}
	return ""
}

// seqArgCallNodes 调用实参里的调用节点（Go 求值顺序：先求实参，再调用）。
// 不穿透函数字面量——字面量只建立函数值，其体内的调用不在此时执行。
func seqArgCallNodes(callNode func(*ast.CallExpr) *CodeSeqNode, call *ast.CallExpr) []*CodeSeqNode {
	var out []*CodeSeqNode
	var walkExpr func(ast.Expr)
	walkExpr = func(e ast.Expr) {
		if e == nil {
			return
		}
		switch x := e.(type) {
		case *ast.FuncLit:
			return // 函数字面量：只建值，不执行体内调用
		case *ast.CallExpr:
			for _, a := range x.Args {
				walkExpr(a)
			}
			walkExpr(x.Fun) // 链式调用 a().b()：receiver 调用先求值
			if n := callNode(x); n != nil {
				out = append(out, n)
			}
		default:
			ast.Inspect(e, func(n ast.Node) bool {
				switch nn := n.(type) {
				case *ast.FuncLit:
					return false
				case *ast.CallExpr:
					for _, a := range nn.Args {
						walkExpr(a)
					}
					walkExpr(nn.Fun)
					if nd := callNode(nn); nd != nil {
						out = append(out, nd)
					}
					return false
				}
				return true
			})
		}
	}
	for _, arg := range call.Args {
		walkExpr(arg)
	}
	// T14：链式调用的接收者调用（`DB.QueryRowContext(...).Scan(&v)` 的
	// QueryRowContext）在索引里是独立 calls 边——也要按准确目标展开，
	// 否则接收者身份/返回类型整体丢失。函数操作数在实参之后走（与既有
	// 实参优先口径一致；不改变已确定的调用顺序语义）。
	walkExpr(call.Fun)
	return out
}

// seqValueActor 合成节点的参与者名（多候选/列表逐项执行时要能区分是哪个
// 函数值，不能渲染成空 actor "->"）。
func seqValueActor(v seqValue) string {
	switch v.kind {
	case seqValueNamed:
		return seqValueShortName(v.symID)
	case seqValueLit:
		return "callback"
	case seqValueList:
		return "list"
	}
	return "unknown"
}
