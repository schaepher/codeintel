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

