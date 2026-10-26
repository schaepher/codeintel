package action

import (
	"go/ast"
	"go/token"
	"strings"
)

// walkEffectPaths 语句列表 → (终态路径写入 out, 返回待续路径)。
func walkEffectPaths(fset *token.FileSet, src []byte, stmts []ast.Stmt, pending []pathSpec,
	out *[]pathSpec, scene Scene, args map[string]string, depth int,
	st *pathWalkStats, budget int) []pathSpec {
	if depth > 12 {
		for i := range pending {
			pending[i].notes = append(pending[i].notes, "分支嵌套过深：未继续展开")
			st.nestTrunc++
		}
		return pending
	}
	span := func(n ast.Node) [2]int {
		return [2]int{fset.Position(n.Pos()).Line, fset.Position(n.End()).Line}
	}
	for _, stmt := range stmts {
		if len(*out) >= budget {
			st.dropped += len(pending)
			markPathBudgetStop(st, fset, src, stmt, len(*out), len(pending))
			return nil
		}
		var next []pathSpec
		for _, cur := range pending {
			switch x := stmt.(type) {
			case *ast.ReturnStmt:
				done := cur
				done.intervals = append(done.intervals, span(x))
				done.returnLine = fset.Position(x.Pos()).Line
				for _, r := range x.Results {
					done.returnVals = append(done.returnVals, strings.TrimSpace(nodeText(fset, src, r)))
					done.returnExprs = append(done.returnExprs, r)
					if id, ok := r.(*ast.Ident); ok && id.Name == "nil" {
						done.returnNil = true
					}
				}
				if len(*out) < budget {
					*out = append(*out, done)
				} else {
					st.dropped++
					markPathBudgetStop(st, fset, src, x, len(*out), 1)
				}
				continue
			case *ast.IfStmt:
				if x.Init != nil {
					cur = applyPathInit(fset, src, cur, x.Init)
				}
				cond := seqExprText(fset, src, x.Cond)
				fit := scene.matchCond(x.Cond, args)
				thenP := cur
				thenP.conditions = append(append([]string{}, cur.conditions...), cond)
				thenP.intervals = append(thenP.intervals, span(x.Body))
				if fit.Status == SceneExcluded {
					thenP.reachable = false
					thenP.notes = append(thenP.notes, "场景排除："+sceneReason(&fit))
				}
				next = append(next, walkEffectPaths(fset, src, x.Body.List, []pathSpec{thenP}, out, scene, args, depth+1, st, budget)...)

				elseP := cur
				elseP.conditions = append(append([]string{}, cur.conditions...), "!("+cond+")")
				if fit.Status == SceneTaken {
					elseP.reachable = false
					elseP.notes = append(elseP.notes, "场景排除："+sceneReason(&fit)+"（else 不成立）")
				}
				switch e := x.Else.(type) {
				case *ast.BlockStmt:
					ep := elseP
					ep.intervals = append(ep.intervals, span(e))
					next = append(next, walkEffectPaths(fset, src, e.List, []pathSpec{ep}, out, scene, args, depth+1, st, budget)...)
				case *ast.IfStmt:
					next = append(next, walkEffectPaths(fset, src, []ast.Stmt{e}, []pathSpec{elseP}, out, scene, args, depth+1, st, budget)...)
				default:
					next = append(next, elseP)
				}
			case *ast.ForStmt, *ast.RangeStmt:
				cur.intervals = append(cur.intervals, span(x))
				cur.notes = append(cur.notes, "循环体：可能执行 0..N 次（写点按可能发生计，未逐次展开）")
				cur.calls = append(cur.calls, collectCallSites(fset, src, x)...)
				st.loops++
				next = append(next, cur)
			case *ast.DeferStmt:
				cur.intervals = append(cur.intervals, span(x))
				cur.notes = append(cur.notes, "defer：函数退出时执行（写点晚于 return 求值）")
				cur.calls = append(cur.calls, collectCallSites(fset, src, x)...)
				next = append(next, cur)
			case *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
				cur.intervals = append(cur.intervals, span(x))
				cur.notes = append(cur.notes, "switch/select：分支未细化为独立路径（写点按可能发生计）")
				cur.calls = append(cur.calls, collectCallSites(fset, src, x)...)
				st.switches++
				next = append(next, cur)
			case *ast.AssignStmt:
				cur.intervals = append(cur.intervals, span(x))
				for i, lhs := range x.Lhs {
					var rhs []ast.Expr
					if len(x.Rhs) == len(x.Lhs) {
						rhs = []ast.Expr{x.Rhs[i]}
					} else {
						rhs = x.Rhs
					}
					switch l := lhs.(type) {
					case *ast.SelectorExpr:
						cur.writes = append(cur.writes, dbWriteSite{field: exprTextShallow(l),
							line: fset.Position(l.Pos()).Line, rhs: rhs})
					case *ast.Ident:
						cur.locals = append(cur.locals, localAssign{name: l.Name,
							line: fset.Position(l.Pos()).Line, rhs: rhs})
					}
				}
				cur.calls = append(cur.calls, collectCallSites(fset, src, x)...)
				next = append(next, cur)
			case *ast.DeclStmt:
				cur.intervals = append(cur.intervals, span(x))
				if gd, ok := x.Decl.(*ast.GenDecl); ok {
					for _, sp := range gd.Specs {
						vs, ok := sp.(*ast.ValueSpec)
						if !ok {
							continue
						}
						for i, name := range vs.Names {
							var rhs []ast.Expr
							if len(vs.Values) == len(vs.Names) {
								rhs = []ast.Expr{vs.Values[i]}
							} else {
								rhs = vs.Values
							}
							cur.locals = append(cur.locals, localAssign{name: name.Name,
								line: fset.Position(name.Pos()).Line, rhs: rhs})
						}
					}
				}
				next = append(next, cur)
			default:
				cur.intervals = append(cur.intervals, span(stmt))
				cur.calls = append(cur.calls, collectCallSites(fset, src, stmt)...)
				next = append(next, cur)
			}
			if len(next) > budget*2 {

				st.dropped += len(next) - budget*2
				markPathBudgetStop(st, fset, src, stmt, len(*out), len(next)-budget*2)
				next = next[:budget*2]
			}
		}
		pending = next
		if len(pending) == 0 {
			return nil
		}
	}
	return pending
}

// markPathBudgetStop 记录首次预算截断的位置/范围/前沿（todo13-7）。
// 只记第一次：后续截断由同一预算上限导致，位置不再有意义。
func markPathBudgetStop(st *pathWalkStats, fset *token.FileSet, src []byte,
	n ast.Node, used, frontier int) {
	if st == nil || n == nil {
		return
	}
	if st.stopLine == 0 {
		st.stopLine = fset.Position(n.Pos()).Line
		text := strings.TrimSpace(nodeText(fset, src, n))
		if len(text) > 120 {
			text = text[:120] + "…"
		}
		st.stopScope = text
	}
	st.budgetUsed = used
	st.frontier += frontier
	st.extraUnknown = true
}

// applyPathInit if/switch 的初始化语句（err := Validate(...)）——两个分支
// 都共享这次赋值（todo12-8：错误来源与路径写入归属需要它）。
func applyPathInit(fset *token.FileSet, src []byte, cur pathSpec, init ast.Stmt) pathSpec {
	span := [2]int{fset.Position(init.Pos()).Line, fset.Position(init.End()).Line}
	cur.intervals = append(cur.intervals, span)
	switch x := init.(type) {
	case *ast.AssignStmt:
		for i, lhs := range x.Lhs {
			var rhs []ast.Expr
			if len(x.Rhs) == len(x.Lhs) {
				rhs = []ast.Expr{x.Rhs[i]}
			} else {
				rhs = x.Rhs
			}
			switch l := lhs.(type) {
			case *ast.Ident:
				cur.locals = append(cur.locals, localAssign{name: l.Name,
					line: fset.Position(l.Pos()).Line, rhs: rhs})
			case *ast.SelectorExpr:
				cur.writes = append(cur.writes, dbWriteSite{field: exprTextShallow(l),
					line: fset.Position(l.Pos()).Line, rhs: rhs})
			}
		}
	}
	cur.calls = append(cur.calls, collectCallSites(fset, src, init)...)
	return cur
}

// collectCallSites 语句内的调用表达式（直接调用；嵌套调用也记录，build 阶段
// 按行号映射索引边）。
func collectCallSites(fset *token.FileSet, src []byte, n ast.Node) []effectCallSite {
	var out []effectCallSite
	ast.Inspect(n, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		cs := effectCallSite{line: fset.Position(call.Lparen).Line,
			name: strings.TrimSpace(nodeText(fset, src, call.Fun))}
		cs.args = append(cs.args, call.Args...)
		out = append(out, cs)
		return true
	})
	return out
}
