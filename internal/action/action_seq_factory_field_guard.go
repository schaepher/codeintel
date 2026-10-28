package action

// todo25 §3.4：工厂 setter 遍历的 switch/if 守卫辅助。

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/token"
	"strings"
)

// seqWalkFieldCalls 按语句顺序遍历，携带 switch/if 守卫。
func seqWalkFieldCalls(n ast.Node, guard string, fn func(*ast.CallExpr, string)) {
	switch x := n.(type) {
	case *ast.BlockStmt:
		for _, st := range x.List {
			seqWalkFieldCalls(st, guard, fn)
		}
	case *ast.IfStmt:
		g := seqGuardAnd(guard, seqNodeText(x.Cond))
		seqWalkFieldCalls(x.Body, g, fn)
		if x.Else != nil {
			seqWalkFieldCalls(x.Else, seqGuardAnd(guard, "!("+seqNodeText(x.Cond)+")"), fn)
		}
	case *ast.SwitchStmt:
		tag := seqNodeText(x.Tag)
		for _, c := range x.Body.List {
			cc, ok := c.(*ast.CaseClause)
			if !ok {
				continue
			}
			g := seqGuardAnd(guard, seqCaseGuard(tag, cc.List))
			for _, st := range cc.Body {
				seqWalkFieldCalls(st, g, fn)
			}
		}
	case *ast.ExprStmt:
		if call, ok := x.X.(*ast.CallExpr); ok {
			fn(call, guard)
		}
	case *ast.AssignStmt:
		for _, r := range x.Rhs {
			if call, ok := r.(*ast.CallExpr); ok {
				fn(call, guard)
			}
		}
	}
}

func seqCaseGuard(tag string, exprs []ast.Expr) string {
	if len(exprs) == 0 {
		return "default"
	}
	parts := make([]string, 0, len(exprs))
	for _, e := range exprs {
		if tag != "" {
			parts = append(parts, tag+" == "+seqNodeText(e))
		} else {
			parts = append(parts, seqNodeText(e))
		}
	}
	return strings.Join(parts, " || ")
}

func seqGuardAnd(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + " && " + b
}

func seqNodeText(e ast.Expr) string {
	if e == nil {
		return ""
	}
	var b bytes.Buffer
	if err := format.Node(&b, token.NewFileSet(), e); err != nil {
		return ""
	}
	return strings.TrimSpace(b.String())
}

func appendUniqueFieldCandidates(in []seqFieldCandidate) []seqFieldCandidate {
	seen := map[string]bool{}
	out := in[:0]
	for _, c := range in {
		key := c.pkgPath + "\x00" + c.typeName + "\x00" + c.guard
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, c)
	}
	return out
}
