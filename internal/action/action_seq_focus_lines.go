package action

import (
	"go/ast"
	"go/token"
)

func (d *focusDep) addAssign(stmt *ast.AssignStmt, lhs ast.Expr, rhs []ast.Expr) {
	line := d.fset.Position(stmt.Pos()).Line
	switch l := lhs.(type) {
	case *ast.Ident:
		if l.Name != "_" {
			d.assigns = append(d.assigns, focusAssign{line: line, pos: stmt.Pos(),
				ident: l.Name, rhs: rhs})
		}
	case *ast.SelectorExpr:
		d.assigns = append(d.assigns, focusAssign{line: line, pos: stmt.Pos(),
			field: exprTextShallow(l), rhs: rhs})
	}
}
func (d *focusDep) addExprs(exprs []ast.Expr, before token.Pos, depth int) {
	for _, e := range exprs {
		d.addExpr(e, before, depth)
	}
}
func exprUsesNames(exprs []ast.Expr, names map[string]bool) bool {
	used := false
	for _, e := range exprs {
		ast.Inspect(e, func(n ast.Node) bool {
			if used {
				return false
			}
			if id, ok := n.(*ast.Ident); ok && names[id.Name] {
				used = true
				return false
			}
			return true
		})
	}
	return used
}

// addName 局部变量/形参的到达定义行（沿当前函数体，不跨函数）。

// addName 局部变量/形参的到达定义行（沿当前函数体，不跨函数）。
func (d *focusDep) addName(name string, before token.Pos, depth int) {
	if name == "" || depth > 6 || before == token.NoPos {
		return
	}
	key := "n:" + name
	if d.seen[key] {
		return
	}
	d.seen[key] = true
	d.names[name] = true
	lines := d.reachingLines(before, func(as focusAssign) bool { return as.ident == name })
	for _, line := range lines {
		d.lines[line] = true
		for _, as := range d.assigns {
			if as.line == line && as.ident == name {
				d.addExprs(as.rhs, as.pos, depth+1)
				break
			}
		}
	}
}

// addField 字段写入的到达定义行（用于返回投影/条件读）。
func (d *focusDep) addField(field string, before token.Pos, depth int) {
	if field == "" || depth > 6 {
		return
	}
	key := "f:" + field
	if d.seen[key] {
		return
	}
	d.seen[key] = true
	for _, line := range d.reachingLines(before, func(as focusAssign) bool { return as.field == field }) {
		d.lines[line] = true
	}
}

// reachingLines 目标位置之前可到达的赋值行（分支并集；循环保守）。
func (d *focusDep) reachingLines(before token.Pos, match func(focusAssign) bool) []int {
	st := &defLineState{}
	walkDefLines(d.fset, d.body.List, before, st, match, 0)
	return st.lines
}

type defLineState struct{ lines []int }

func (s *defLineState) clone() *defLineState {
	out := &defLineState{lines: make([]int, len(s.lines))}
	copy(out.lines, s.lines)
	return out
}
func walkDefLines(fset *token.FileSet, stmts []ast.Stmt, target token.Pos, st *defLineState,
	match func(focusAssign) bool, depth int) bool {
	if depth > 12 {
		return false
	}
	for _, s := range stmts {
		if target != token.NoPos {
			if target < s.Pos() {
				return false
			}
			if target <= s.End() {
				return walkDefLineStmt(fset, s, target, st, match, depth)
			}
		}
		applyDefLineStmt(fset, s, st, match, depth)
	}
	return false
}
func walkDefLineStmt(fset *token.FileSet, stmt ast.Stmt, target token.Pos, st *defLineState,
	match func(focusAssign) bool, depth int) bool {
	switch x := stmt.(type) {
	case *ast.BlockStmt:
		return walkDefLines(fset, x.List, target, st, match, depth+1)
	case *ast.IfStmt:
		if target >= x.Body.Pos() && target <= x.Body.End() {
			return walkDefLines(fset, x.Body.List, target, st, match, depth+1)
		}
		if x.Else != nil && target >= x.Else.Pos() && target <= x.Else.End() {
			return walkDefLineStmt(fset, x.Else, target, st, match, depth)
		}
		return true
	case *ast.ForStmt:
		return walkDefLines(fset, x.Body.List, target, st, match, depth+1)
	case *ast.RangeStmt:
		return walkDefLines(fset, x.Body.List, target, st, match, depth+1)
	case *ast.SwitchStmt:
		return walkDefLines(fset, x.Body.List, target, st, match, depth+1)
	case *ast.TypeSwitchStmt:
		return walkDefLines(fset, x.Body.List, target, st, match, depth+1)
	case *ast.SelectStmt:
		return walkDefLines(fset, x.Body.List, target, st, match, depth+1)
	}
	return true
}
func applyDefLineStmt(fset *token.FileSet, stmt ast.Stmt, st *defLineState,
	match func(focusAssign) bool, depth int) {
	if depth > 12 {
		return
	}
	setAt := func(as focusAssign) {
		if match(as) {
			st.lines = []int{as.line}
		}
	}
	switch x := stmt.(type) {
	case *ast.AssignStmt:
		line := fset.Position(x.Pos()).Line
		if len(x.Lhs) == len(x.Rhs) {
			for i, lhs := range x.Lhs {
				setAt(focusAssign{line: line, ident: identName(lhs), field: fieldText(lhs),
					rhs: []ast.Expr{x.Rhs[i]}})
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
			for _, name := range vs.Names {
				setAt(focusAssign{line: fset.Position(name.Pos()).Line, ident: name.Name})
			}
		}
	case *ast.BlockStmt:
		walkDefLines(fset, x.List, token.NoPos, st, match, depth+1)
	case *ast.IfStmt:
		thenE := st.clone()
		walkDefLines(fset, x.Body.List, token.NoPos, thenE, match, depth+1)
		elseE := st.clone()
		if x.Else != nil {
			applyDefLineStmt(fset, x.Else, elseE, match, depth+1)
		}
		st.lines = dedupInts(append(thenE.lines, elseE.lines...))
	case *ast.ForStmt:
		loopE := st.clone()
		walkDefLines(fset, x.Body.List, token.NoPos, loopE, match, depth+1)
		st.lines = dedupInts(append(st.lines, loopE.lines...))
	case *ast.RangeStmt:
		loopE := st.clone()
		walkDefLines(fset, x.Body.List, token.NoPos, loopE, match, depth+1)
		st.lines = dedupInts(append(st.lines, loopE.lines...))
	case *ast.SwitchStmt:
		st.lines = defLineCases(fset, x.Body, st, match, depth)
	case *ast.TypeSwitchStmt:
		st.lines = defLineCases(fset, x.Body, st, match, depth)
	case *ast.SelectStmt:
		st.lines = defLineCases(fset, x.Body, st, match, depth)
	}
}
func defLineCases(fset *token.FileSet, body *ast.BlockStmt, st *defLineState,
	match func(focusAssign) bool, depth int) []int {
	out := append([]int{}, st.lines...)
	for _, c := range body.List {
		cl, ok := c.(*ast.CaseClause)
		if !ok {
			continue
		}
		e := st.clone()
		walkDefLines(fset, cl.Body, token.NoPos, e, match, depth+1)
		out = append(out, e.lines...)
	}
	return dedupInts(out)
}
func identName(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}
func fieldText(e ast.Expr) string {
	if s, ok := e.(*ast.SelectorExpr); ok {
		return exprTextShallow(s)
	}
	return ""
}
func dedupInts(xs []int) []int {
	seen := map[int]bool{}
	out := make([]int, 0, len(xs))
	for _, x := range xs {
		if x == 0 || seen[x] {
			continue
		}
		seen[x] = true
		out = append(out, x)
	}
	return out
}
