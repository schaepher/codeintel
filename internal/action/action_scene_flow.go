package action

// todo11-2：场景条件的**值流传播**（有界）与实现候选选择。
//
// 用户确认的 Q3/Q4：
//   - 自建"字面量来源解析器"：实参 → 变量链（同函数赋值）→ 形参（向上找
//     调用点实参），深度上限 2；非字面量即停（unknown:depth / unknown:value）
//   - **禁止按字段名相似推断同值**：只有同一变量/形参链、同一实参位置才算
//   - 接口/实现候选：若候选来自场景守卫的构造函数/赋值（同一函数内
//     `if cfg.X == "v" { x = A{} } else { x = B{} }`），按场景选候选并把
//     依据记进 evidence（reason=scene-guard:<k=v>）；无法证明 → 保持候选
//     集合 + ambiguous（不静默选第一个）

import (
	"go/ast"
	"go/token"
	"strings"
)

// maxSceneFlowDepth 值流解析深度（一层变量 + 一层形参；超出即 unknown）。
const maxSceneFlowDepth = 2

// sceneArg 实参解析结果（值 + 来源证据——todo12-3 R3）。
type sceneArg struct {
	val    string // 已知字面量；空 = 未知
	source string // 定义位置/传播依据（如 def@L20、scene-arg@L22）
}

// resolveSceneArgLiterals 解析一次调用的实参字面量（位置对齐；解析不到为空）。
//
// 值来源只沿**当前展开路径**：函数内变量链（`x := "CN"`）+ 本次调用的入站
// 实参（incoming，按形参位置）。**绝不**去"任意调用者"里找同名实参——那会把
// 别的分支的字面量串进来（本轮实测：DispatchDyn 的 region 被 Dispatch 的
// "CN" 误判为 taken）。
func (a *Actions) resolveSceneArgLiterals(fset *token.FileSet, src []byte, call *ast.CallExpr,
	body *ast.BlockStmt, params *ast.FieldList, incoming, incomingSrc []string, depth int) []sceneArg {
	if call == nil {
		return nil
	}
	out := make([]sceneArg, 0, len(call.Args))
	for _, arg := range call.Args {
		out = append(out, resolveLiteralExpr(fset, src, body, params, incoming, incomingSrc, arg, depth))
	}
	return out
}

// resolveLiteralExpr 表达式 → (字面量值, 来源)；无法判定时值为空。
func resolveLiteralExpr(fset *token.FileSet, src []byte, body *ast.BlockStmt,
	params *ast.FieldList, incoming, incomingSrc []string, expr ast.Expr, depth int) sceneArg {
	if depth < 0 {
		return sceneArg{}
	}
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind.String() == "STRING" {
			return sceneArg{val: strings.Trim(e.Value, "\"`"),
				source: "literal@" + fileLine("", fset.Position(e.Pos()).Line)}
		}
		return sceneArg{}
	case *ast.ParenExpr:
		return resolveLiteralExpr(fset, src, body, params, incoming, incomingSrc, e.X, depth)
	case *ast.Ident:
		// todo12-3：解析调用点**之前实际可到达**的定义（无条件覆盖、
		// 条件合流、作用域遮蔽、定义顺序）；不扫别的函数补值。
		v, line, known, found := reachingLiteralAt(fset, src, body, expr.Pos(), e.Name, params, incoming)
		if known {
			src := "def@L" + itoaSeq(line)
			return sceneArg{val: v, source: src}
		}
		if found {
			// 有定义但无法证明唯一值：不得回退场景键（todo12-3 R2）
			src := "unknown:arg-value"
			if line > 0 {
				src += "@L" + itoaSeq(line)
			}
			return sceneArg{val: sceneArgUnknown, source: src}
		}
		if idx := paramIndex(params, e.Name); idx >= 0 && idx < len(incoming) {
			src := "arg@" + fileLine("", fset.Position(expr.Pos()).Line)
			if idx < len(incomingSrc) && incomingSrc[idx] != "" {
				src = incomingSrc[idx] + " → " + src
			}
			return sceneArg{val: incoming[idx], source: src}
		}
		return sceneArg{}
	}
	// SelectorExpr（req.Region）/ 调用 / 其他：静态不可判定——**不猜**
	return sceneArg{}
}

// sceneArgUnknown 传播实参的"无法证明唯一值"哨兵：调用点有定义但不是
// 唯一字面量时，内层不得回退到 --scene 键当成已证明（todo12-3 R2）。
const sceneArgUnknown = "\x00unknown"

// litVal 抽象值：已知字符串字面量 / 未知（含声明存在但值不可判定）。
type litVal struct {
	val   string
	known bool
	line  int // 定义位置（赋值/声明行——证据）
}

// litScopes 词法作用域栈（形参/局部变量 → 抽象值）。
type litScopes struct {
	scopes []map[string]litVal
}

func newLitScopes(params *ast.FieldList, incoming []string) *litScopes {
	s := &litScopes{scopes: []map[string]litVal{{}}}
	if params == nil {
		return s
	}
	i := 0
	for _, f := range params.List {
		nn := len(f.Names)
		if nn == 0 {
			nn = 1
		}
		for j := 0; j < nn; j++ {
			name := ""
			if j < len(f.Names) {
				name = f.Names[j].Name
			}
			if name != "" {
				v := litVal{}
				if i < len(incoming) && incoming[i] != "" {
					v = litVal{val: incoming[i], known: true}
				}
				s.scopes[0][name] = v
			}
			i++
		}
	}
	return s
}

func (s *litScopes) push() { s.scopes = append(s.scopes, map[string]litVal{}) }
func (s *litScopes) pop()  { s.scopes = s.scopes[:len(s.scopes)-1] }

func (s *litScopes) lookup(name string) (litVal, bool) {
	for i := len(s.scopes) - 1; i >= 0; i-- {
		if v, ok := s.scopes[i][name]; ok {
			return v, true
		}
	}
	return litVal{}, false
}

// assign 更新最近的既有声明；没有声明则落在最内层作用域。
func (s *litScopes) assign(name string, v litVal) {
	for i := len(s.scopes) - 1; i >= 0; i-- {
		if _, ok := s.scopes[i][name]; ok {
			s.scopes[i][name] = v
			return
		}
	}
	s.scopes[len(s.scopes)-1][name] = v
}

// declare 在当前作用域声明（:= / var 的遮蔽语义）。
func (s *litScopes) declare(name string, v litVal) {
	s.scopes[len(s.scopes)-1][name] = v
}

func (s *litScopes) clone() *litScopes {
	out := &litScopes{scopes: make([]map[string]litVal, len(s.scopes))}
	for i, sc := range s.scopes {
		m := make(map[string]litVal, len(sc))
		for k, v := range sc {
			m[k] = v
		}
		out.scopes[i] = m
	}
	return out
}

// assignMerged 多分支合流：所有分支同值才保留，否则未知（保守，不猜）。
func assignMerged(dst *litScopes, branches ...*litScopes) {
	if len(branches) == 0 || len(branches[0].scopes) == 0 {
		return
	}
	dst.scopes = make([]map[string]litVal, len(branches[0].scopes))
	for i := range branches[0].scopes {
		m := map[string]litVal{}
		for k, v := range branches[0].scopes[i] {
			m[k] = v
		}
		dst.scopes[i] = m
	}
	for _, b := range branches[1:] {
		for i := range dst.scopes {
			if i >= len(b.scopes) {
				continue
			}
			for k, cur := range dst.scopes[i] {
				v, ok := b.scopes[i][k]
				if !ok || cur.known != v.known || cur.val != v.val {
					dst.scopes[i][k] = litVal{}
				}
			}
		}
	}
}

// reachingLiteralAt 调用点之前 name 的可达字面量（同函数体，作用域感知）。
func reachingLiteralAt(fset *token.FileSet, src []byte, body *ast.BlockStmt, target token.Pos,
	name string, params *ast.FieldList, incoming []string) (string, int, bool, bool) {
	if body == nil {
		return "", 0, false, false
	}
	s := newLitScopes(params, incoming)
	walkReachingStmts(fset, src, body.List, target, s, 0)
	v, ok := s.lookup(name)
	if !ok {
		return "", 0, false, false
	}
	if !v.known {
		return "", v.line, false, true
	}
	return v.val, v.line, true, true
}

// walkReachingStmts 顺序执行语句直到 target（返回 reached=true 表示 target
// 落在该语句列表中且已处理到它）。
func walkReachingStmts(fset *token.FileSet, src []byte, stmts []ast.Stmt, target token.Pos,
	s *litScopes, depth int) bool {
	if depth > 12 {
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
	return true // 其他语句（ExprStmt/Assign/Return...）里出现 target：停止
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

// applyReachingStmt target 之前的语句 → 更新抽象环境。
func applyReachingStmt(fset *token.FileSet, src []byte, st ast.Stmt, s *litScopes, depth int) {
	if depth > 12 {
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
			}
		}
	case *ast.AssignStmt:
		if len(x.Lhs) != len(x.Rhs) {
			for _, lhs := range x.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && id.Name != "_" {
					s.assign(id.Name, litVal{})
				}
			}
			return
		}
		for i, lhs := range x.Lhs {
			id, ok := lhs.(*ast.Ident)
			if !ok || id.Name == "_" {
				continue
			}
			v := evalLitExpr(fset, x.Rhs[i], s)
			if x.Tok == token.DEFINE {
				s.declare(id.Name, v)
			} else {
				s.assign(id.Name, v)
			}
		}
	case *ast.BlockStmt:
		s.push()
		walkReachingStmts(fset, src, x.List, token.NoPos, s, depth+1)
		s.pop()
	case *ast.IfStmt:
		thenE := s.clone()
		thenE.push()
		walkReachingStmts(fset, src, x.Body.List, token.NoPos, thenE, depth+1)
		thenE.pop()
		if x.Else == nil {
			assignMerged(s, thenE, s.clone()) // 无 else：分支不成立仍保留原值
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
		assignMerged(s, loopE, s.clone()) // 循环可能执行 0 次
	case *ast.SwitchStmt:
		mergeReachingClauses(fset, src, x.Body, s, depth)
	case *ast.TypeSwitchStmt:
		mergeReachingClauses(fset, src, x.Body, s, depth)
	case *ast.SelectStmt:
		mergeReachingClauses(fset, src, x.Body, s, depth)
	}
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

// evalLitExpr 表达式 → 抽象值（字符串字面量 / 已知变量；其余未知）。
func evalLitExpr(fset *token.FileSet, e ast.Expr, s *litScopes) litVal {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind == token.STRING {
			return litVal{val: strings.Trim(x.Value, "\"`"), known: true,
				line: fset.Position(x.Pos()).Line}
		}
	case *ast.Ident:
		if v, ok := s.lookup(x.Name); ok {
			return v
		}
	case *ast.ParenExpr:
		return evalLitExpr(fset, x.X, s)
	}
	return litVal{}
}

// paramIndex 形参名在签名中的位置（跳过 receiver）。
func paramIndex(params *ast.FieldList, name string) int {
	if params == nil {
		return -1
	}
	i := 0
	for _, f := range params.List {
		nn := len(f.Names)
		if nn == 0 {
			nn = 1
		}
		for j := 0; j < nn; j++ {
			if j < len(f.Names) && f.Names[j].Name == name {
				return i
			}
			i++
		}
	}
	return -1
}

// lastSymName canonical ID 末段（方法形态 (T).M 取 M 之前的调用名匹配用）。
func lastSymName(id string) string {
	if i := strings.LastIndex(id, ":"); i >= 0 {
		id = id[i+1:]
	}
	if i := strings.LastIndex(id, ")."); i >= 0 {
		return id[i+2:]
	}
	return id
}

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
		state, why := evalImplGuards(d, req.Scene, req.sceneArgs, req.sceneSrc)
		switch state {
		case SceneExcluded:
			continue // 该定义在当前场景下不可达
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
		// 多个定义都被场景判定为可达 → 不能唯一确定
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
func evalImplGuards(d implDef, scene Scene, args, srcs map[string]string) (string, string) {
	if len(d.guards) == 0 {
		return SceneTaken, ""
	}
	state, why := SceneTaken, ""
	for _, g := range d.guards {
		fit := scene.matchCondSrc(g.cond, args, srcs)
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

func (s *implState) clone() *implState {
	out := &implState{defs: make([]implDef, len(s.defs))}
	copy(out.defs, s.defs)
	return out
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
			if !ok {
				continue
			}
			if t := implTypeFromExpr(x.Rhs[i]); t != "" {
				st.defs = []implDef{{impl: t, line: fset.Position(x.Pos()).Line,
					guards: append([]sceneGuard{}, guards...)}}
			} else if id.Name != "_" {
				// 赋了非字面量实现（变量/参数/调用）：不再有已知实现
				st.defs = nil
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
				if name.Name == "_" {
					continue
				}
				if i < len(vs.Values) {
					if t := implTypeFromExpr(vs.Values[i]); t != "" {
						st.defs = []implDef{{impl: t, line: fset.Position(name.Pos()).Line,
							guards: append([]sceneGuard{}, guards...)}}
						continue
					}
				}
				st.defs = nil // var 声明（无字面量实现）
			}
		}
	case *ast.BlockStmt:
		walkImplStmts(fset, x.List, token.NoPos, st, guards, depth+1)
	case *ast.IfStmt:
		thenE := st.clone()
		walkImplStmts(fset, x.Body.List, token.NoPos, thenE, appendGuard(guards, x.Cond, true), depth+1)
		if x.Else == nil {
			// 无 else：分支不成立时保留原定义
			st.defs = mergeImplDefs(st.defs, thenE.defs)
			return
		}
		elseE := st.clone()
		applyImplStmt(fset, x.Else, elseE, appendGuard(guards, x.Cond, false), depth+1)
		st.defs = mergeImplDefs(thenE.defs, elseE.defs)
	case *ast.ForStmt:
		loopE := st.clone()
		walkImplStmts(fset, x.Body.List, token.NoPos, loopE, guards, depth+1)
		st.defs = mergeImplDefs(st.defs, loopE.defs) // 循环可能 0 次
	case *ast.RangeStmt:
		loopE := st.clone()
		walkImplStmts(fset, x.Body.List, token.NoPos, loopE, guards, depth+1)
		st.defs = mergeImplDefs(st.defs, loopE.defs)
	case *ast.SwitchStmt:
		mergeImplClauses(fset, x.Body, st, guards, depth)
	case *ast.TypeSwitchStmt:
		mergeImplClauses(fset, x.Body, st, guards, depth)
	case *ast.SelectStmt:
		mergeImplClauses(fset, x.Body, st, guards, depth)
	}
}

func mergeImplClauses(fset *token.FileSet, body *ast.BlockStmt, st *implState, guards []sceneGuard, depth int) {
	base := st.defs
	var all []implDef
	hasDefault := false
	for _, c := range body.List {
		cl, ok := c.(*ast.CaseClause)
		if !ok {
			continue
		}
		if cl.List == nil {
			hasDefault = true
		}
		e := &implState{defs: append([]implDef{}, base...)}
		walkImplStmts(fset, cl.Body, token.NoPos, e, guards, depth+1)
		all = append(all, e.defs...)
	}
	if !hasDefault {
		all = append(all, base...)
	}
	st.defs = dedupeImplDefs(all)
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
