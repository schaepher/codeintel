package action

import (
	"go/ast"
	"go/token"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// focusLinesForFunc 焦点模式下的关注行：SSA 字段写入行 + 目标值的到达定义
// （初始化/条件改写/必要调用），排除与目标无值依赖的调用（todo12-9 R1）。
func (a *Actions) focusLinesForFunc(funcID domain.CanonicalID, fn *ast.FuncDecl,
	fset *token.FileSet, src []byte, f *seqFocus) (map[int]bool, int) {
	if f == nil || fn == nil || fn.Body == nil {
		return nil, 0
	}
	d := &focusDep{fset: fset, body: fn.Body, lines: map[int]bool{},
		names: map[string]bool{}, seen: map[string]bool{}, resolved: f.fields,
		varTypes: effectVarTypes(fset, fn, src), pkgPath: pkgPathOf(string(funcID))}
	if len(f.fields) > 0 {
		d.resolved = append(d.resolved, f.fieldIDs...)
	}
	d.collect()

	for _, as := range d.assigns {
		if as.ident != "" && focusNameHit(as.ident, f.fields) {
			d.lines[as.line] = true
			d.names[as.ident] = true
			d.addExprs(as.rhs, as.pos, 0)
		}
	}

	if len(f.fields) > 0 {
		if rows, err := a.repo.GetFunctionFields(funcID); err == nil {
			for _, r := range rows {
				if r.AccessKind != domain.SummaryDirectWrite && r.AccessKind != domain.SummaryIndirectWrite {
					continue
				}
				if !focusFieldHit(r.FieldPath, f.fieldIDs) {
					continue
				}
				if r.LineStart > 0 {
					d.lines[r.LineStart] = true
				}
			}
		}
		for _, as := range d.assigns {
			if as.field == "" {
				continue
			}
			if !d.fieldSelected(as.field) {
				continue
			}
			d.lines[as.line] = true
			d.addExprs(as.rhs, as.pos, 0)
		}
	}

	if f.returns {
		for _, r := range d.returns {
			d.addExpr(r, r.Pos(), 0)
		}

		d.markReturnAliasDeps()
	}
	d.fixpoint()
	if len(d.lines) == 0 {
		return nil, d.unknown
	}
	return d.lines, d.unknown
}

// markReturnAliasDeps 返回焦点的别名/被调依赖（todo13-9）：
//   - tracked = 返回表达式引用的变量 + 由它们派生的别名（alias := first）
//   - 跟踪变量的定义行 → 保留（实例定义/别名依据）
//   - 实参引用 tracked 的调用行 → 保留（被调可能改写该实例）
//   - 无返回值的被调展开（void callee）→ 保留其字段写入行；是否展开由
//     调用点是否命中决定，因此不会把无关实例的调用带进来。
func (d *focusDep) markReturnAliasDeps() {
	tracked := map[string]bool{}
	for _, r := range d.returns {
		ast.Inspect(r, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && id.Name != "_" && id.Name != "nil" {
				tracked[id.Name] = true
			}
			return true
		})
	}

	for round := 0; round < 5; round++ {
		changed := false
		for _, as := range d.assigns {
			if as.ident == "" || tracked[as.ident] || !exprUsesNames(as.rhs, tracked) {
				continue
			}
			tracked[as.ident] = true
			changed = true
		}
		if !changed {
			break
		}
	}
	for _, as := range d.assigns {
		if !tracked[as.ident] {
			continue
		}
		d.lines[as.line] = true
		d.names[as.ident] = true
		d.addExprs(as.rhs, as.pos, 1)
	}

	if d.body != nil {
		ast.Inspect(d.body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			for _, arg := range call.Args {
				if exprUsesNames([]ast.Expr{arg}, tracked) {
					d.lines[d.fset.Position(call.Lparen).Line] = true
					break
				}
			}
			return true
		})
	}

	if len(d.returns) == 0 {
		for _, as := range d.assigns {
			if as.field != "" {
				d.lines[as.line] = true
			}
		}
	}
}

// fieldSelected AST 字段文本是否命中焦点目标（todo12-9 R2：类型身份优先，
// 同名不同类型不串线；类型未知保守保留并计 partial）。
func (d *focusDep) fieldSelected(text string) bool {
	leaf := text
	if i := strings.LastIndex(leaf, "."); i >= 0 {
		leaf = leaf[i+1:]
	}
	if len(d.resolved) > 0 {
		if id, known := writeIdentity(text, d.varTypes, d.pkgPath); known {
			for _, want := range d.resolved {
				if want == id {
					return true
				}
			}
			return false
		}
	}
	if focusLeafHit(leaf, d.resolved) {
		if _, known := writeIdentity(text, d.varTypes, d.pkgPath); !known && len(d.resolved) > 0 {
			d.unknown++
		}
		return true
	}
	return false
}

// collect 收集赋值/条件/返回语句。
func (d *focusDep) collect() {
	ast.Inspect(d.body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			if len(x.Lhs) == len(x.Rhs) {
				for i, lhs := range x.Lhs {
					d.addAssign(x, lhs, []ast.Expr{x.Rhs[i]})
				}
			} else {
				for _, lhs := range x.Lhs {
					d.addAssign(x, lhs, x.Rhs)
				}
			}
		case *ast.DeclStmt:
			gd, ok := x.Decl.(*ast.GenDecl)
			if !ok {
				return true
			}
			for _, sp := range gd.Specs {
				vs, ok := sp.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					var rhs []ast.Expr
					if len(vs.Values) == len(vs.Names) {
						rhs = []ast.Expr{vs.Values[i]}
					}
					d.assigns = append(d.assigns, focusAssign{
						line: d.fset.Position(name.Pos()).Line, pos: name.Pos(),
						ident: name.Name, rhs: rhs})
				}
			}
		case *ast.IfStmt:
			d.ifs = append(d.ifs, focusIf{pos: x.Pos(), cond: x.Cond,
				ranges: [][2]int{{
					d.fset.Position(x.Body.Lbrace).Line,
					d.fset.Position(x.Body.Rbrace).Line}}})
		case *ast.ReturnStmt:
			for _, r := range x.Results {
				d.returns = append(d.returns, r)
			}
		}
		return true
	})

	ast.Inspect(d.body, func(n ast.Node) bool {
		ifs, ok := n.(*ast.IfStmt)
		if !ok || ifs.Else == nil {
			return true
		}
		start := d.fset.Position(ifs.Body.Lbrace).Line
		end := d.fset.Position(ifs.Else.End()).Line
		for i := range d.ifs {
			if d.ifs[i].pos == ifs.Pos() {
				d.ifs[i].ranges = [][2]int{{start, end}}
			}
		}
		return true
	})
}

// addExpr 表达式依赖：变量/字段的到达定义 + 表达式内调用。
func (d *focusDep) addExpr(e ast.Expr, before token.Pos, depth int) {
	if e == nil || depth > 6 {
		return
	}
	ast.Inspect(e, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			if x.Name != "_" && x.Name != "nil" && x.Name != "true" && x.Name != "false" {
				d.addName(x.Name, before, depth+1)
			}
		case *ast.SelectorExpr:
			text := exprTextShallow(x)
			if d.fieldSelected(text) && x.Pos() < before {
				d.addField(text, before, depth+1)
			}
		case *ast.CallExpr:
			d.lines[d.fset.Position(x.Lparen).Line] = true
		}
		return true
	})
}

// fixpoint 字段写入/条件依赖补全（被返回变量影响的状态写入、条件里的调用）。
func (d *focusDep) fixpoint() {
	for round := 0; round < 5; round++ {
		changed := false
		for _, as := range d.assigns {
			if as.field == "" || d.lines[as.line] {
				continue
			}
			if exprUsesNames(as.rhs, d.names) {
				d.lines[as.line] = true
				d.addExprs(as.rhs, as.pos, 1)
				changed = true
			}
		}
		for _, iff := range d.ifs {
			inRange := false
			for _, rg := range iff.ranges {
				for line := range d.lines {
					if line >= rg[0] && line <= rg[1] {
						inRange = true
						break
					}
				}
				if inRange {
					break
				}
			}
			if inRange {
				before := len(d.lines)
				d.addExpr(iff.cond, iff.pos, 1)
				if len(d.lines) != before {
					changed = true
				}
			}
		}
		if !changed {
			return
		}
	}
}
