package action

// 运行时表达式求值：字面量/常量辅助（从 runtime_eval_expr.go 拆出）。

import (
	"go/ast"
	"go/token"
	"strconv"
)

func evalRuntimeLiteral(lit *ast.BasicLit) rtValue {
	switch lit.Kind {
	case token.INT:
		i, err := strconv.ParseInt(lit.Value, 0, 64)
		if err != nil {
			return rtUnknownValue()
		}
		return rtIntValue(i)
	case token.FLOAT:
		f, err := strconv.ParseFloat(lit.Value, 64)
		if err != nil {
			return rtUnknownValue()
		}
		return rtFloatValue(f)
	case token.STRING:
		s, err := strconv.Unquote(lit.Value)
		if err != nil {
			return rtUnknownValue()
		}
		return rtStringValue(s)
	case token.CHAR:
		s, err := strconv.Unquote(lit.Value)
		if err != nil || len(s) == 0 {
			return rtUnknownValue()
		}
		return rtIntValue(int64([]rune(s)[0]))
	}
	return rtUnknownValue()
}
