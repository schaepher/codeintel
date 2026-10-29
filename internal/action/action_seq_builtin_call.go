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
