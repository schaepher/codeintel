package action

import "go/ast"

// seqReturnValuesOf 收集函数体（不含嵌套字面量体）里每个 return 表达式的
// 函数值——条件返回 → 多候选（不任选一条）。
func seqReturnValuesOf(a *Actions, req CodeSequenceRequest, fileCtx *seqFileCtx,
	fn *ast.FuncDecl, env *seqEnv, call *ast.CallExpr, depth int) ([]seqValue, bool) {
	if fn == nil || fn.Body == nil || depth >= seqMaxFactoryDepth {
		return nil, false
	}
	// T03：条件返回按**调用点实参/场景**裁剪——`Wrap(false, Hit)` 只走
	// `return f`，包装闭包（GuardedA/B）不得被当"另一条可达链"执行。
	consts := seqConstValues(a, req, fileCtx.rel)
	args := seqCalleeArgLiterals(fn, call, consts)
	var vals []seqValue
	for _, site := range seqReturnSitesOf(fn) {
		if !seqReturnGuardsAllow(site.guards, args, consts) {
			continue
		}
		for _, r := range site.exprs {
			sub := req
			sub.env = env
			vals = append(vals, captureSeqClosureValueDepth(a, sub, fileCtx, r, depth+1))
		}
	}
	return vals, len(vals) > 0
}

// seqReturnSite 一个 return 语句 + 到达它需满足的守卫（if/else 极性）。
type seqReturnSite struct {
	exprs  []ast.Expr
	guards []seqReturnGuard
}

// seqReturnSitesOf 收集函数体（不含嵌套字面量体）里的 return 及其守卫路径。
func seqReturnSitesOf(fn *ast.FuncDecl) []seqReturnSite {
	if fn == nil || fn.Body == nil {
		return nil
	}
	var sites []seqReturnSite
	var walk func(stmts []ast.Stmt, guards []seqReturnGuard)
	walk = func(stmts []ast.Stmt, guards []seqReturnGuard) {
		for _, st := range stmts {
			switch s := st.(type) {
			case *ast.ReturnStmt:
				if len(s.Results) == 0 {
					continue
				}
				sites = append(sites, seqReturnSite{exprs: s.Results, guards: guards})
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
					// guard 子句（`if k { return A }` 后无 else）：后续语句只在
					// !k 下可达。
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
				// T04：值 switch 的 case 作为守卫（tag 已知时裁剪）；无法解析
				// tag/值 → 全部保留（保守）。
				guards = seqWalkSwitchSites(s, guards, walk)
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
	return sites
}

// seqReturnGuard 工厂返回的到达条件（if cond body 上的 return → want=true）。
// T04：switch case 的守卫以"tag == case 值"表示（field/lit/hasCase），
// 调用点实参已知时同样裁剪。
type seqReturnGuard struct {
	cond ast.Expr
	want bool

	field   string // switch tag 的字段名（如 kind / req.Kind）
	lit     string // case 值
	hasCase bool   // true = 该 return 位于此 case 分支
}

// seqReturnGuardsAllow 路径上全部守卫在已知实参下都可满足 → 该 return 可达。
// 任一守卫被排除 → 不可达；未知 → 保守保留（可达）。
func seqReturnGuardsAllow(guards []seqReturnGuard, args map[string]string, consts sceneConstLookup) bool {
	if len(guards) == 0 {
		return true
	}
	var sc Scene
	for _, g := range guards {
		if g.hasCase {
			v, ok := args[g.field]
			if !ok {
				continue // tag 值未知 → 保守保留
			}
			if sceneValueEqual(g.lit, v) != g.want {
				return false
			}
			continue
		}
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

// seqWalkSwitchSites 值 switch → 每个 case 子句的 return 带 case 守卫。
// tag 不可解析时退回"全部保留"。
func seqWalkSwitchSites(s *ast.SwitchStmt, guards []seqReturnGuard,
	walk func([]ast.Stmt, []seqReturnGuard)) []seqReturnGuard {
	if s.Tag == nil {
		walk(s.Body.List, guards)
		return guards
	}
	field, ok := sceneFieldOf(s.Tag)
	if !ok {
		walk(s.Body.List, guards)
		return guards
	}
	hasDefault := false
	allTerminate := true
	var caseLits []string
	for _, st := range s.Body.List {
		cc, ok := st.(*ast.CaseClause)
		if !ok {
			continue
		}
		if len(cc.List) == 0 { // default：未知覆盖，保守保留
			hasDefault = true
			walk(cc.Body, guards)
			if !seqStmtsTerminate(cc.Body) {
				allTerminate = false
			}
			continue
		}
		if !seqStmtsTerminate(cc.Body) {
			allTerminate = false
		}
		for _, ce := range cc.List {
			lit, ok := sceneValueOf(ce, nil)
			if !ok {
				walk(cc.Body, guards)
				allTerminate = false
				continue
			}
			caseLits = append(caseLits, lit)
			g := append(append([]seqReturnGuard(nil), guards...),
				seqReturnGuard{field: field, lit: lit, hasCase: true, want: true})
			walk(cc.Body, g)
		}
	}
	// 无 default 且各 case 都返回：switch 之后的语句只在"tag 不是任何 case
	// 值"时可达（`switch kind { case 1: return One }; return nil`）。
	if !hasDefault && allTerminate {
		for _, lit := range caseLits {
			guards = append(guards, seqReturnGuard{field: field, lit: lit, hasCase: true, want: false})
		}
	}
	return guards
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
