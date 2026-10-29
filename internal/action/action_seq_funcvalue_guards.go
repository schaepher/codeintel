package action

import "go/ast"

// seqReturnValuesOf 收集函数体（不含嵌套字面量体）里每个 return 表达式的
// 函数值——条件返回 → 多候选（不任选一条）。
func seqReturnValuesOf(a *Actions, req CodeSequenceRequest, fileCtx *seqFileCtx,
	fn *ast.FuncDecl, env *seqEnv, call *ast.CallExpr, depth int) ([]seqValue, bool) {
	if fn == nil || fn.Body == nil || depth >= seqMaxFactoryDepth {
		return nil, false
	}

	consts := seqConstValues(a, req, fileCtx.rel)
	args := seqCalleeArgLiterals(fn, call, consts)
	var vals []seqValue
	var walk func(stmts []ast.Stmt, guards []seqReturnGuard)
	walk = func(stmts []ast.Stmt, guards []seqReturnGuard) {
		for _, st := range stmts {
			switch s := st.(type) {
			case *ast.ReturnStmt:
				if !seqReturnGuardsAllow(guards, args, consts) {
					continue
				}
				for _, r := range s.Results {
					sub := req
					sub.env = env
					vals = append(vals, captureSeqClosureValueDepth(a, sub, fileCtx, r, depth+1))
				}
			case *ast.IfStmt:
				walk(s.Body.List, append(guards, seqReturnGuard{cond: s.Cond, want: true}))
				if s.Else != nil {
					switch e := s.Else.(type) {
					case *ast.BlockStmt:
						walk(e.List, append(guards, seqReturnGuard{cond: s.Cond, want: false}))
					case *ast.IfStmt:
						walk([]ast.Stmt{e}, append(guards, seqReturnGuard{cond: s.Cond, want: false}))
					}
				} else if seqStmtsTerminate(s.Body.List) {

					guards = append(guards, seqReturnGuard{cond: s.Cond, want: false})
				}
			case *ast.BlockStmt:
				walk(s.List, guards)
			case *ast.LabeledStmt:
				walk([]ast.Stmt{s.Stmt}, guards)
			case *ast.ForStmt:
				walk(s.Body.List, guards)
			case *ast.RangeStmt:
				walk(s.Body.List, guards)
			case *ast.SwitchStmt:

				walk(s.Body.List, guards)
			case *ast.TypeSwitchStmt:
				walk(s.Body.List, guards)
			case *ast.CaseClause:
				walk(s.Body, guards)
			case *ast.CommClause:
				walk(s.Body, guards)
			}
		}
	}
	walk(fn.Body.List, nil)
	return vals, len(vals) > 0
}

// seqReturnGuard 工厂返回的到达条件（if cond body 上的 return → want=true）。
type seqReturnGuard struct {
	cond ast.Expr
	want bool
}

// seqReturnGuardsAllow 路径上全部守卫在已知实参下都可满足 → 该 return 可达。
// 任一守卫被排除 → 不可达；未知 → 保守保留（可达）。
func seqReturnGuardsAllow(guards []seqReturnGuard, args map[string]string, consts sceneConstLookup) bool {
	if len(guards) == 0 {
		return true
	}
	var sc Scene
	for _, g := range guards {
		fit := sc.matchCondSrc(g.cond, args, nil, consts)
		if g.want && fit.Status == SceneExcluded {
			return false
		}
		if !g.want && fit.Status == SceneTaken {
			return false
		}
	}
	return true
}

// seqCalleeArgLiterals 被调工厂的形参 → 调用点实参字面量（只认可静态求值
// 的实参；未知保持缺失 = 该守卫不裁剪）。
func seqCalleeArgLiterals(fn *ast.FuncDecl, call *ast.CallExpr, consts sceneConstLookup) map[string]string {
	if fn == nil || fn.Type.Params == nil || call == nil || len(call.Args) == 0 {
		return nil
	}
	out := map[string]string{}
	idx := 0
	for _, f := range fn.Type.Params.List {
		n := len(f.Names)
		if n == 0 {
			n = 1
		}
		for i := 0; i < n; i++ {
			if idx >= len(call.Args) {
				return out
			}
			if i < len(f.Names) && f.Names[i].Name != "" {
				if v, ok := sceneValueOf(call.Args[idx], consts); ok {
					out[f.Names[i].Name] = v
				}
			}
			idx++
		}
	}
	return out
}

// seqStmtsTerminate 语句列表是否必然终止（return / panic；if-else 两支都
// 终止）。用于 guard 子句后的可达性（后续 return 受 !cond 约束）。
func seqStmtsTerminate(stmts []ast.Stmt) bool {
	if len(stmts) == 0 {
		return false
	}
	switch s := stmts[len(stmts)-1].(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.BlockStmt:
		return seqStmtsTerminate(s.List)
	case *ast.IfStmt:
		if s.Else == nil {
			return false
		}
		var elseStmts []ast.Stmt
		switch e := s.Else.(type) {
		case *ast.BlockStmt:
			elseStmts = e.List
		case *ast.IfStmt:
			elseStmts = []ast.Stmt{e}
		}
		return seqStmtsTerminate(s.Body.List) && seqStmtsTerminate(elseStmts)
	}
	return false
}
