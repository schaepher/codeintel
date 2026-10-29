package action

import "go/ast"

// seqPureBuiltinCall 纯内建调用（无回调执行语义）：append/len/cap/copy/make/
// new/delete/close/print/println/complex/real/imag/clear/min/max。
// 只在标识符未被局部变量/函数遮蔽时成立（Obj==nil = 预声明标识符）。
func seqPureBuiltinCall(fun ast.Expr) bool {
	id, ok := fun.(*ast.Ident)
	if !ok || id.Obj != nil {
		return false
	}
	switch id.Name {
	case "append", "len", "cap", "copy", "make", "new", "delete", "close",
		"print", "println", "complex", "real", "imag", "clear", "min", "max":
		return true
	}
	return false
}

// seqPureTypeConversionCall 单实参类型转换（Fun 是类型对象/指针类型表达式）
// ——不是代码图里的调用。
func seqPureTypeConversionCall(call *ast.CallExpr) bool {
	if call == nil || len(call.Args) != 1 {
		return false
	}
	return seqExprIsTypeConversion(call.Fun)
}
