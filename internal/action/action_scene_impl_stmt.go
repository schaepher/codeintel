package action

// todo22 P1：实现候选的到达定义——**按声明对象**分别保存（同名变量/遮蔽
// 不串），并区分"没有定义"（键不存在）与"定义为未知"（键存在、值为空）。

import (
	"go/ast"
	"go/token"
	"strings"
)

func applyImplStmt(fset *token.FileSet, stmt ast.Stmt, st *implState, guards []sceneGuard, depth int) {
	if depth > 12 {
		return
	}
	switch x := stmt.(type) {
	case *ast.AssignStmt:
		if len(x.Lhs) != len(x.Rhs) {
			return
		}
		for i, lhs := range x.Lhs {
			id, ok := lhs.(*ast.Ident)
			if !ok || id.Name == "_" || id.Obj == nil {
				continue
			}
			if t := implTypeFromExpr(x.Rhs[i]); t != "" {
				st.set(id.Obj, []implDef{{impl: t, line: fset.Position(x.Pos()).Line,
					guards: append([]sceneGuard{}, guards...)}})
			} else {
				st.set(id.Obj, nil) // 明确未知：不再沿用旧实现
			}
		}
	case *ast.DeclStmt:
		gd, ok := x.Decl.(*ast.GenDecl)
		if !ok {
			return
		}
		for _, sp := range gd.Specs {
			vs, ok := sp.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if name.Name == "_" || name.Obj == nil {
					continue
				}
				if i < len(vs.Values) {
					if t := implTypeFromExpr(vs.Values[i]); t != "" {
						st.set(name.Obj, []implDef{{impl: t, line: fset.Position(name.Pos()).Line,
							guards: append([]sceneGuard{}, guards...)}})
						continue
					}
					st.set(name.Obj, nil)
					continue
				}
				// `var d Doer`：变量存在但没有任何实现定义
				st.set(name.Obj, nil)
			}
		}
	case *ast.BlockStmt:
		// 块内新变量按对象身份加入；对外层变量的赋值照常覆盖外层键
		walkImplStmts(fset, x.List, token.NoPos, st, guards, depth+1)
	case *ast.IfStmt:
		thenE := st.clone()
		walkImplStmts(fset, x.Body.List, token.NoPos, thenE, appendGuard(guards, x.Cond, true), depth+1)
		elseE := st.clone()
		if x.Else != nil {
			applyImplStmt(fset, x.Else, elseE, appendGuard(guards, x.Cond, false), depth+1)
		}
		*st = *mergeImplStates(thenE, elseE)
	case *ast.ForStmt:
		loopE := st.clone()
		walkImplStmts(fset, x.Body.List, token.NoPos, loopE, guards, depth+1)
		*st = *mergeImplStates(st.clone(), loopE) // 循环可能执行 0 次
	case *ast.RangeStmt:
		loopE := st.clone()
		walkImplStmts(fset, x.Body.List, token.NoPos, loopE, guards, depth+1)
		*st = *mergeImplStates(st.clone(), loopE)
	case *ast.SwitchStmt:
		mergeImplClauses(fset, x.Body, st, guards, depth)
	case *ast.TypeSwitchStmt:
		mergeImplClauses(fset, x.Body, st, guards, depth)
	case *ast.SelectStmt:
		mergeImplClauses(fset, x.Body, st, guards, depth)
	}
}

func mergeImplClauses(fset *token.FileSet, body *ast.BlockStmt, st *implState, guards []sceneGuard, depth int) {
	base := st.clone()
	var branches []*implState
	hasDefault := false
	for _, c := range body.List {
		cl, ok := c.(*ast.CaseClause)
		if !ok {
			continue
		}
		if cl.List == nil {
			hasDefault = true
		}
		e := base.clone()
		walkImplStmts(fset, cl.Body, token.NoPos, e, guards, depth+1)
		branches = append(branches, e)
	}
	if !hasDefault {
		branches = append(branches, base)
	}
	if len(branches) == 0 {
		return
	}
	*st = *mergeImplStates(branches...)
}

func appendGuard(gs []sceneGuard, cond ast.Expr, positive bool) []sceneGuard {
	out := make([]sceneGuard, len(gs), len(gs)+1)
	copy(out, gs)
	return append(out, sceneGuard{cond: cond, positive: positive})
}

func mergeImplDefs(a, b []implDef) []implDef {
	return dedupeImplDefs(append(append([]implDef{}, a...), b...))
}

func dedupeImplDefs(defs []implDef) []implDef {
	seen := map[string]bool{}
	out := make([]implDef, 0, len(defs))
	for _, d := range defs {
		key := d.impl + ":" + itoaSeq(d.line) + ":" + itoaSeq(len(d.guards))
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, d)
	}
	return out
}

// implTypeFromExpr 赋值右值 → 实现类型名（&T{} / T{} / NewT()）。
func implTypeFromExpr(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.UnaryExpr:
		return implTypeFromExpr(x.X)
	case *ast.CompositeLit:
		if id, ok := x.Type.(*ast.Ident); ok {
			return id.Name
		}
		if sel, ok := x.Type.(*ast.SelectorExpr); ok {
			return sel.Sel.Name
		}
	case *ast.CallExpr:
		name := ""
		switch fun := x.Fun.(type) {
		case *ast.Ident:
			name = fun.Name
		case *ast.SelectorExpr:
			name = fun.Sel.Name
		}
		if name != "" && strings.HasPrefix(name, "new") && len(name) > 3 {
			return name[3:]
		}
		if name != "" && strings.HasPrefix(name, "New") && len(name) > 3 {
			return name[3:]
		}
	}
	return ""
}
