package action

// todo22 §2.1：函数值捕获的**统一入口**——所有调用点（普通调用、外部/未证明
// 调用、未解析分支、测试调用者）都只通过这些函数取值，不允许各自直连底层
// 求值函数（否则"到达定义/绑定口径"会分叉）。
//
// 三个入口与职责：
//   - captureSeqClosureValue：单个函数值（字面量/变量/形参/包级函数/方法值/
//     工厂返回），失败时返回**明确的 unknown**（带原因），不返回零值占位。
//   - captureSeqClosureArgs：一次调用的实参值（按位置；`f(list...)` 摊平）。
//   - captureSeqCallbackLists：函数列表（[]func 字面量/append/变参/range 对象）。
//
// 绑定元数据随值一起保存（todo22 §2.1）：词法文件与 AST（seqFileCtx）、包
// （pkgPath）、receiver（recvType/recvDecl）、声明对象（declObj）、调用上下文
// （bindFile/bindLine）、条件（cond），并用 `*ast.Object` 身份区分同名阴影。

import (
	"go/ast"

	"github.com/schaepher/codeintel/internal/domain"
)

// captureSeqClosureValue 捕获单个表达式的函数值（唯一入口）。
func captureSeqClosureValue(a *Actions, req CodeSequenceRequest, ctx *seqFileCtx, expr ast.Expr) seqValue {
	return captureSeqClosureValueDepth(a, req, ctx, expr, 0)
}

// captureSeqClosureValueDepth 带工厂递归深度上限的捕获（内部递归用；避免
// 自引用工厂无限展开）。
func captureSeqClosureValueDepth(a *Actions, req CodeSequenceRequest, ctx *seqFileCtx, expr ast.Expr, depth int) seqValue {
	v := seqValueOfExprDepth(a, req, ctx, req.env, expr, depth)
	// 方法值：x.Method——接收者类型来自本函数内按**对象身份**的类型数据流
	// （构造器/字面量赋值），不按变量名全局搜同名赋值。
	if v.kind == seqValueUnknown {
		if id, ok := expr.(*ast.SelectorExpr); ok {
			if mv, ok2 := seqMethodValue(a, req, ctx, id); ok2 {
				return mv
			}
		}
	}
	return v
}

// captureSeqClosureArgs 捕获一次调用的实参值（形参绑定用）。
func captureSeqClosureArgs(a *Actions, req CodeSequenceRequest, ctx *seqFileCtx, call *ast.CallExpr) []seqValue {
	if call == nil {
		return nil
	}
	out := make([]seqValue, 0, len(call.Args))
	for i, arg := range call.Args {
		v := captureSeqClosureValue(a, req, ctx, arg)
		v.origin = seqOriginArg
		v.bindFile, v.bindLine = req.curFile, lineOfOffset(ctx, call.Lparen)
		// todo22 §2.4：`f(list...)` 把列表元素摊平为实参
		if call.Ellipsis.IsValid() && i == len(call.Args)-1 {
			if v.kind == seqValueList {
				out = append(out, v.elems...)
				continue
			}
			// T02：多候选列表（条件分支 append）整体传给变参形参——保持候选
			// 边界，交给 range 多列表路径（公共前缀确定 + 差异尾部互斥）。
			if seqValueIsFuncList(v) {
				out = append(out, v)
				continue
			}
		}
		out = append(out, v)
	}
	return out
}

// captureSeqCallbackLists 捕获"函数列表"表达式（字面量/append/变量绑定）。
func captureSeqCallbackLists(a *Actions, req CodeSequenceRequest, ctx *seqFileCtx, expr ast.Expr) (seqValue, bool) {
	if expr == nil {
		return seqValue{}, false
	}
	if id, ok := expr.(*ast.Ident); ok {
		if req.env != nil {
			if v, ok2 := req.env.lookup(id.Obj); ok2 && v.kind == seqValueList {
				return v, true
			}
		}
		return seqValue{}, false
	}
	v := captureSeqClosureValue(a, req, ctx, expr)
	if !seqValueIsFuncList(v) {
		return seqValue{}, false
	}
	return v, true
}

// seqValueIsFuncList 列表值，或"每个候选都是列表"的多候选值（T02：条件
// 分支 append 得到 multi(list…)，仍需按候选列表展开元素）。
func seqValueIsFuncList(v seqValue) bool {
	if v.kind == seqValueList {
		return true
	}
	if v.kind != seqValueMulti || len(v.cands) == 0 {
		return false
	}
	for _, c := range v.cands {
		if c.kind != seqValueList {
			return false
		}
	}
	return true
}

// seqMethodValue `x.Method` 的方法值：接收者类型 = 变量在本函数内的类型数据流。
// 只接受**同一声明对象**的类型（阴影/无关实例不借用）；符号必须真实存在于索引。
func seqMethodValue(a *Actions, req CodeSequenceRequest, ctx *seqFileCtx, sel *ast.SelectorExpr) (seqValue, bool) {
	var ref seqTypeRef
	var decl *ast.Object
	if id, ok := sel.X.(*ast.Ident); ok {
		if id.Obj != nil && id.Obj.Kind == ast.Pkg {
			return seqValue{}, false
		}
		r, ok2 := seqLookupType(req.env, id.Obj)
		if !ok2 {
			return seqValue{}, false
		}
		ref, decl = r, id.Obj
	} else {
		// T02：`(&S{}).M` / `(NewS()).M`——接收者是表达式时按类型来源解析
		// （复合字面量/构造器/括号），不按变量名猜测。
		r, ok2 := seqTypeOfExpr(a, req, ctx, sel.X)
		if !ok2 {
			return seqValue{}, false
		}
		ref = r
	}
	symID := "symbol:go:" + ref.pkgPath + ":(" + ref.typeName + ")." + sel.Sel.Name
	if _, err := a.repo.GetSymbol(domain.CanonicalID(symID)); err != nil {
		return unknownSeqValue("方法值 " + ref.typeName + "." + sel.Sel.Name +
			" 在索引中不存在（receiver 类型来自类型数据流，但方法未索引）"), true
	}
	v := namedSeqValue(symID)
	v.bindFile, v.bindLine = req.curFile, lineOfOffset(ctx, sel.Sel.Pos())
	v.declObj = decl
	return v, true
}

// seqTypeOfExpr 表达式的接收者类型（构造器/字面量/变量链；纯语法数据流）。

// unwrapParen 剥掉括号（`(*T)` → `*T`）。

// seqTypeRefOf 类型表达式 → 类型引用（Ident / pkg.T / 泛型 T[P]）。

// seqLookupType 按声明对象查类型（沿闭包父环境）。

// lineOfOffset 位置 → 行号（ctx 可能为 nil）。

// seqReceiverTypeRef 方法调用接收者的类型（`extT.Method()` / `h.store.Method()`）：
//  1. 局部变量：按声明对象查类型数据流（声明/复合字面量/构造器返回）；
//  2. 当前 receiver 的字段：按结构体声明取字段类型（`s.store.Method()`）；
//  3. 已知类型的变量字段：同文件结构体声明（不做跨文件猜测）。

// seqStructFieldTypeRef 同文件结构体声明的字段类型（含 pkg.T / *T / 泛型）。

// seqExternalFuncResultType 外部包包级函数/构造器的第一个返回类型。
