package action

// B10：选择器候选值与静态求值（从 action_seq_coverage.go 拆出）。

import (
	"go/ast"
	"go/token"
)

// seqCollectSelectorValues 从条件表达式收集各形参被比较的候选值。
func seqCollectSelectorValues(e ast.Expr, params []string, consts map[string]string,
	out map[string][]string) {
	ast.Inspect(e, func(n ast.Node) bool {
		b, ok := n.(*ast.BinaryExpr)
		if !ok || (b.Op != token.EQL && b.Op != token.NEQ) {
			return true
		}
		check := func(x, y ast.Expr) {
			id, ok := runtimeUnparen(x).(*ast.Ident)
			if !ok {
				return
			}
			for _, p := range params {
				if id.Name != p {
					continue
				}
				if v, ok := seqLiteralString(y, consts); ok {
					out[p] = append(out[p], v)
				}
			}
		}
		check(b.X, b.Y)
		check(b.Y, b.X)
		return true
	})
}

// seqEvalSelectorExpr 静态求值（只支持布尔/比较/字面量/常量/形参）。
func seqEvalSelectorExpr(e ast.Expr, consts map[string]string, env map[string]string) (string, bool) {
	switch v := runtimeUnparen(e).(type) {
	case *ast.ParenExpr:
		return seqEvalSelectorExpr(v.X, consts, env)
	case *ast.UnaryExpr:
		if v.Op == token.NOT {
			s, ok := seqEvalSelectorExpr(v.X, consts, env)
			if !ok {
				return "", false
			}
			if s == "true" {
				return "false", true
			}
			return "true", true
		}
	case *ast.BinaryExpr:
		switch v.Op {
		case token.LAND:
			l, lok := seqEvalSelectorExpr(v.X, consts, env)
			r, rok := seqEvalSelectorExpr(v.Y, consts, env)
			if lok && l == "false" || rok && r == "false" {
				return "false", true
			}
			if lok && rok {
				return boolStr(l == "true" && r == "true"), true
			}
			return "", false
		case token.LOR:
			l, lok := seqEvalSelectorExpr(v.X, consts, env)
			r, rok := seqEvalSelectorExpr(v.Y, consts, env)
			if lok && l == "true" || rok && r == "true" {
				return "true", true
			}
			if lok && rok {
				return boolStr(l == "true" || r == "true"), true
			}
			return "", false
		case token.EQL, token.NEQ:
			l, lok := seqSelectorOperand(v.X, consts, env)
			r, rok := seqSelectorOperand(v.Y, consts, env)
			if !lok || !rok {
				return "", false
			}
			eq := l == r
			if v.Op == token.EQL {
				return boolStr(eq), true
			}
			return boolStr(!eq), true
		}
	}
	if s, ok := seqSelectorOperand(e, consts, env); ok {
		if s == "true" || s == "false" {
			return s, true
		}
	}
	return "", false
}

// seqSelectorOperand 操作数文本（字面量/常量/形参值）。
func seqSelectorOperand(e ast.Expr, consts map[string]string, env map[string]string) (string, bool) {
	if id, ok := runtimeUnparen(e).(*ast.Ident); ok {
		if v, ok := env[id.Name]; ok {
			return v, true
		}
	}
	return seqLiteralString(e, consts)
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
