package action

import (
	"go/ast"
	"go/token"
)

// sceneGuard 一条分支假设（cond 为真 / 为假）。
type sceneGuard struct {
	cond     ast.Expr
	positive bool
}

// implDef 一个到达调用点的实现定义（带守卫）。
type implDef struct {
	impl   string
	line   int
	guards []sceneGuard
}

// sceneImplChoice 接口调用的实现选择（todo11-2 / todo12-4）：
//   - 按**调用点的到达定义**收集实现赋值（无条件覆盖、if/else 合并、
//     嵌套 else-if、未初始化路径），守卫用场景判定；
//   - 已知场景能唯一确定 → 选它，reason=scene-guard:<依据>；
//   - 否则保留候选集合（调用方标 ambiguous，不静默把第一个当结论）。
//
// 只分析当前函数体（req.curBody），不扫别的函数。
func (a *Actions) sceneImplChoice(req CodeSequenceRequest, fset *token.FileSet, callOffset int,
	file *ast.File, varName, methodName, pkgPath string) (impl, reason string, cands []string, guard bool, ok bool) {
	body := req.curBody
	if body == nil || varName == "" || methodName == "" {
		return "", "", nil, false, false
	}
	defs := reachingImplsAt(fset, body, token.Pos(callOffset), varName)
	if len(defs) == 0 {
		return "", "", nil, false, false
	}
	sel := map[string]bool{}
	var selected []implDef
	for _, d := range defs {
		state, why := evalImplGuards(d, req.Scene, req.sceneArgs, req.sceneSrc, req.sceneConsts)
		switch state {
		case SceneExcluded:
			continue
		case SceneTaken:
			selected = append(selected, d)
			sel[d.impl] = true
			if why != "" {
				reason = "scene-guard:" + why
			} else {
				reason = "reaching-def:L" + itoaSeq(d.line)
			}
		case SceneUnknown:
			sel[d.impl] = true
		}
	}
	for implName := range sel {
		cands = append(cands, "symbol:go:"+pkgPath+":("+implName+")."+methodName)
	}
	sortStrings(cands)
	if len(selected) == 1 {
		return "symbol:go:" + pkgPath + ":(" + selected[0].impl + ")." + methodName,
			reason, cands, true, true
	}
	if len(selected) > 1 {

		return "symbol:go:" + pkgPath + ":(" + selected[0].impl + ")." + methodName,
			"", cands, false, true
	}
	if len(cands) == 0 {
		return "", "", nil, false, false
	}
	return cands[0], "", cands, false, true
}

// evalImplGuards 用场景判定定义守卫：任何守卫 excluded → 定义不可达；
// 全部 taken → 定义必然可达；否则 unknown。
func evalImplGuards(d implDef, scene Scene, args, srcs map[string]string, consts sceneConstLookup) (string, string) {
	if len(d.guards) == 0 {
		return SceneTaken, ""
	}
	state, why := SceneTaken, ""
	for _, g := range d.guards {
		fit := scene.matchCondSrc(g.cond, args, srcs, consts)
		st := fit.Status
		if !g.positive {
			switch st {
			case SceneTaken:
				st = SceneExcluded
			case SceneExcluded:
				st = SceneTaken
			}
		}
		if st == SceneExcluded {
			return SceneExcluded, ""
		}
		if st == SceneUnknown {
			state = SceneUnknown
		}
		part := sceneReason(&fit)
		if !g.positive {
			part = "!(" + part + ")"
		}
		if why != "" {
			why += " && " + part
		} else {
			why = part
		}
	}
	if state == SceneTaken {
		return SceneTaken, why
	}
	return SceneUnknown, why
}

// reachingImplsAt 调用点之前 varName 的到达实现定义（含分支守卫）。
func reachingImplsAt(fset *token.FileSet, body *ast.BlockStmt, target token.Pos, varName string) []implDef {
	st := &implState{}
	walkImplStmts(fset, body.List, target, st, nil, 0)
	return st.defs
}

// implState 到达定义集合（赋值替换；分支合并）。
type implState struct {
	defs []implDef
}

func walkImplStmts(fset *token.FileSet, stmts []ast.Stmt, target token.Pos, st *implState,
	guards []sceneGuard, depth int) bool {
	if depth > 12 {
		return false
	}
	for _, s := range stmts {
		if target != token.NoPos {
			if target < s.Pos() {
				return false
			}
			if target <= s.End() {
				return walkImplStmt(fset, s, target, st, guards, depth)
			}
		}
		applyImplStmt(fset, s, st, guards, depth)
	}
	return false
}
func walkImplStmt(fset *token.FileSet, stmt ast.Stmt, target token.Pos, st *implState,
	guards []sceneGuard, depth int) bool {
	switch x := stmt.(type) {
	case *ast.BlockStmt:
		return walkImplStmts(fset, x.List, target, st, guards, depth+1)
	case *ast.IfStmt:
		if target >= x.Cond.Pos() && target <= x.Cond.End() {
			return true
		}
		if target >= x.Body.Pos() && target <= x.Body.End() {
			g := appendGuard(guards, x.Cond, true)
			return walkImplStmts(fset, x.Body.List, target, st, g, depth+1)
		}
		if x.Else != nil && target >= x.Else.Pos() && target <= x.Else.End() {
			g := appendGuard(guards, x.Cond, false)
			return walkImplStmt(fset, x.Else, target, st, g, depth+1)
		}
		return true
	case *ast.ForStmt:
		if target >= x.Body.Pos() && target <= x.Body.End() {
			return walkImplStmts(fset, x.Body.List, target, st, guards, depth+1)
		}
		return true
	case *ast.RangeStmt:
		if target >= x.Body.Pos() && target <= x.Body.End() {
			return walkImplStmts(fset, x.Body.List, target, st, guards, depth+1)
		}
		return true
	case *ast.SwitchStmt:
		return walkImplClauses(fset, x.Body, target, st, guards, depth)
	case *ast.TypeSwitchStmt:
		return walkImplClauses(fset, x.Body, target, st, guards, depth)
	case *ast.SelectStmt:
		return walkImplClauses(fset, x.Body, target, st, guards, depth)
	}
	return true
}
func walkImplClauses(fset *token.FileSet, body *ast.BlockStmt, target token.Pos, st *implState,
	guards []sceneGuard, depth int) bool {
	for _, c := range body.List {
		cl, ok := c.(*ast.CaseClause)
		if !ok {
			continue
		}
		if target < cl.Pos() || target > cl.End() {
			continue
		}
		return walkImplStmts(fset, cl.Body, target, st, guards, depth+1)
	}
	return true
}
