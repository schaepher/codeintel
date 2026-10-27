package action

import (
	"go/ast"
	"go/token"
)

// walkReachingStmt 处理包含 target 的语句（只走实际到达该语句的分支）。
func walkReachingStmt(fset *token.FileSet, src []byte, st ast.Stmt, target token.Pos,
	s *litScopes, depth int) bool {
	switch x := st.(type) {
	case *ast.BlockStmt:
		s.push()
		reached := walkReachingStmts(fset, src, x.List, target, s, depth+1)
		s.pop()
		return reached
	case *ast.IfStmt:
		if target >= x.Cond.Pos() && target <= x.Cond.End() {
			return true
		}
		if target >= x.Body.Pos() && target <= x.Body.End() {
			s.push()
			reached := walkReachingStmts(fset, src, x.Body.List, target, s, depth+1)
			s.pop()
			return reached
		}
		if x.Else != nil && target >= x.Else.Pos() && target <= x.Else.End() {
			return walkReachingStmt(fset, src, x.Else, target, s, depth)
		}
		return true
	case *ast.ForStmt:
		if target >= x.Body.Pos() && target <= x.Body.End() {
			s.push()
			reached := walkReachingStmts(fset, src, x.Body.List, target, s, depth+1)
			s.pop()
			return reached
		}
		return true
	case *ast.RangeStmt:
		if target >= x.Body.Pos() && target <= x.Body.End() {
			s.push()
			reached := walkReachingStmts(fset, src, x.Body.List, target, s, depth+1)
			s.pop()
			return reached
		}
		return true
	case *ast.SwitchStmt:
		return walkReachingClauses(fset, src, x.Body, target, s, depth)
	case *ast.TypeSwitchStmt:
		return walkReachingClauses(fset, src, x.Body, target, s, depth)
	case *ast.SelectStmt:
		return walkReachingClauses(fset, src, x.Body, target, s, depth)
	}
	return true
}
func walkReachingClauses(fset *token.FileSet, src []byte, body *ast.BlockStmt, target token.Pos,
	s *litScopes, depth int) bool {
	for _, c := range body.List {
		cl, ok := c.(*ast.CaseClause)
		if !ok || cl == nil {
			continue
		}
		if target < cl.Pos() || target > cl.End() {
			continue
		}
		s.push()
		reached := walkReachingStmts(fset, src, cl.Body, target, s, depth+1)
		s.pop()
		return reached
	}
	return true
}

// mergeReachingClauses switch/select 分支合流（同值保留；否则未知）。
func mergeReachingClauses(fset *token.FileSet, src []byte, body *ast.BlockStmt, s *litScopes, depth int) {
	base := s.clone()
	var branches []*litScopes
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
		e.push()
		walkReachingStmts(fset, src, cl.Body, token.NoPos, e, depth+1)
		e.pop()
		branches = append(branches, e)
	}
	if !hasDefault {
		branches = append(branches, base)
	}
	assignMerged(s, branches...)
}

// walkReachingStmts 顺序执行语句直到 target（返回 reached=true 表示 target
// 落在该语句列表中且已处理到它）。
func walkReachingStmts(fset *token.FileSet, src []byte, stmts []ast.Stmt, target token.Pos,
	s *litScopes, depth int) bool {
	if depth > 12 {
		s.limited = true
		return false
	}
	for _, st := range stmts {
		if target != token.NoPos {
			if target < st.Pos() {
				return false
			}
			if target <= st.End() {
				return walkReachingStmt(fset, src, st, target, s, depth)
			}
		}
		applyReachingStmt(fset, src, st, s, depth)
	}
	return false
}

// applyReachingStmt target 之前的语句 → 更新抽象环境。
func applyReachingStmt(fset *token.FileSet, src []byte, st ast.Stmt, s *litScopes, depth int) {
	if depth > 12 {
		s.limited = true
		return
	}
	switch x := st.(type) {
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
				v := litVal{}
				if len(vs.Values) == len(vs.Names) {
					v = evalLitExpr(fset, vs.Values[i], s)
				}
				s.declare(name.Name, v)

				if len(vs.Values) == len(vs.Names) {
					declareStructFields(fset, s, name.Name, vs.Values[i])
				}
			}
		}
	case *ast.AssignStmt:

		for _, rhs := range x.Rhs {
			if _, isLit := rhs.(*ast.BasicLit); !isLit {
				applyCallInvalidation(fset, src, rhs, s)
			}
		}
		if len(x.Lhs) != len(x.Rhs) {
			for _, lhs := range x.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && id.Name != "_" {
					s.assign(id.Name, litVal{})
				}
			}
			return
		}
		for i, lhs := range x.Lhs {

			if sel, ok := lhs.(*ast.SelectorExpr); ok {
				if id, ok2 := sel.X.(*ast.Ident); ok2 && id.Name != "_" {
					s.assign(exprTextShallow(sel), evalLitExpr(fset, x.Rhs[i], s))
				}
				continue
			}
			id, ok := lhs.(*ast.Ident)
			if !ok || id.Name == "_" {
				continue
			}
			v := evalLitExpr(fset, x.Rhs[i], s)
			if x.Tok == token.DEFINE {
				s.declare(id.Name, v)
				declareStructFields(fset, s, id.Name, x.Rhs[i])
			} else {
				s.assign(id.Name, v)

				clearStructFields(s, id.Name)
				declareStructFields(fset, s, id.Name, x.Rhs[i])
			}
		}
	case *ast.BlockStmt:
		s.push()
		walkReachingStmts(fset, src, x.List, token.NoPos, s, depth+1)
		s.pop()
	case *ast.ExprStmt:

		applyCallInvalidation(fset, src, x.X, s)
	case *ast.GoStmt:
		applyCallInvalidation(fset, src, x.Call, s)
	case *ast.DeferStmt:
		applyCallInvalidation(fset, src, x.Call, s)
	case *ast.IfStmt:
		thenE := s.clone()
		thenE.push()
		walkReachingStmts(fset, src, x.Body.List, token.NoPos, thenE, depth+1)
		thenE.pop()
		if x.Else == nil {
			assignMerged(s, thenE, s.clone())
			return
		}
		elseE := s.clone()
		applyReachingStmt(fset, src, x.Else, elseE, depth+1)
		assignMerged(s, thenE, elseE)
	case *ast.ForStmt, *ast.RangeStmt:
		loopE := s.clone()
		body := (*ast.BlockStmt)(nil)
		if f, ok := x.(*ast.ForStmt); ok {
			body = f.Body
		} else if r, ok := x.(*ast.RangeStmt); ok {
			body = r.Body
		}
		if body != nil {
			loopE.push()
			walkReachingStmts(fset, src, body.List, token.NoPos, loopE, depth+1)
			loopE.pop()
		}
		assignMerged(s, loopE, s.clone())
	case *ast.SwitchStmt:
		mergeReachingClauses(fset, src, x.Body, s, depth)
	case *ast.TypeSwitchStmt:
		mergeReachingClauses(fset, src, x.Body, s, depth)
	case *ast.SelectStmt:
		mergeReachingClauses(fset, src, x.Body, s, depth)
	}
}
