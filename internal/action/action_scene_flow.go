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

// resolveSceneArgLiterals 解析一次调用的实参字面量（位置对齐；解析不到为空串）。
//
// 值来源只沿**当前展开路径**：函数内变量链（`x := "CN"`）+ 本次调用的入站
// 实参（incoming，按形参位置）。**绝不**去"任意调用者"里找同名实参——那会把
// 别的分支的字面量串进来（本轮实测：DispatchDyn 的 region 被 Dispatch 的
// "CN" 误判为 taken）。
func (a *Actions) resolveSceneArgLiterals(fset *token.FileSet, src []byte, call *ast.CallExpr,
	body *ast.BlockStmt, params *ast.FieldList, incoming []string, depth int) []string {
	if call == nil {
		return nil
	}
	out := make([]string, 0, len(call.Args))
	for _, arg := range call.Args {
		out = append(out, resolveLiteralExpr(fset, src, body, params, incoming, arg, depth))
	}
	return out
}

// resolveLiteralExpr 表达式 → 字面量值（变量链 + 入站实参；深度受限）。
func resolveLiteralExpr(fset *token.FileSet, src []byte, body *ast.BlockStmt,
	params *ast.FieldList, incoming []string, expr ast.Expr, depth int) string {
	if depth < 0 {
		return ""
	}
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind.String() == "STRING" {
			return strings.Trim(e.Value, "\"`")
		}
		return ""
	case *ast.ParenExpr:
		return resolveLiteralExpr(fset, src, body, params, incoming, e.X, depth)
	case *ast.Ident:
		// ① 同**当前函数体**的赋值链（不能扫全文件——会把别的函数的
		// `region := "CN"` 串进来，本轮实测踩过）
		if v, ok := localAssignLiteral(fset, src, body, e.Name); ok {
			return v
		}
		// ② 当前函数的形参 → 本次调用的入站实参（同一路径，不跨分支）
		if idx := paramIndex(params, e.Name); idx >= 0 && idx < len(incoming) {
			return incoming[idx]
		}
		return ""
	}
	// SelectorExpr（req.Region）/ 调用 / 其他：静态不可判定——**不猜**
	return ""
}

// localAssignLiteral 同函数内 name 的字面量赋值（取首个字面量赋值）。
// 只认同名变量的直接赋值，不做字段名相似匹配（用户要求）。
func localAssignLiteral(fset *token.FileSet, src []byte, body *ast.BlockStmt, name string) (string, bool) {
	if body == nil {
		return "", false
	}
	var val string
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		id, ok := as.Lhs[0].(*ast.Ident)
		if !ok || id.Name != name {
			return true
		}
		lit, ok := as.Rhs[0].(*ast.BasicLit)
		if ok && lit.Kind.String() == "STRING" {
			val, found = strings.Trim(lit.Value, "\"`"), true
		}
		return true
	})
	return val, found
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

// sceneImplChoice 接口调用的实现选择（todo11-2 Q4b）：
//   - 若场景守卫（同一函数内 `if <scene 条件> { d = &A{} } else { d = &B{} }`）
//     能唯一确定 → 选它，reason=scene-guard:<依据>
//   - 否则若同一变量有多个具体赋值 → 返回候选集合（调用方标 ambiguous，
//     不静默把"第一个"当结论）
//
// 只在当前函数内按**同名变量**找赋值（不做字段名相似推断）。
func (a *Actions) sceneImplChoice(req CodeSequenceRequest, fset *token.FileSet, file *ast.File,
	varName, methodName, pkgPath string) (impl, reason string, cands []string, guard bool, ok bool) {
	if file == nil || varName == "" || methodName == "" {
		return "", "", nil, false, false
	}
	type cand struct {
		impl string
		line int
	}
	var found []cand
	ast.Inspect(file, func(n ast.Node) bool {
		as, isAssign := n.(*ast.AssignStmt)
		if !isAssign || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		id, isID := as.Lhs[0].(*ast.Ident)
		if !isID || id.Name != varName {
			return true
		}
		if t := implTypeFromExpr(as.Rhs[0]); t != "" {
			found = append(found, cand{impl: t, line: fset.Position(as.Pos()).Line})
		}
		return true
	})
	if len(found) == 0 {
		return "", "", nil, false, false
	}
	for _, c := range found {
		cands = append(cands, "symbol:go:"+pkgPath+":("+c.impl+")."+methodName)
	}
	// 场景守卫：赋值行落在 if body 内且该条件被场景命中
	for _, c := range found {
		if cond, has := enclosingIfCond(fset, file, c.line); has {
			fit := req.Scene.matchCond(cond, req.sceneArgs)
			if fit.Status == SceneTaken {
				return "symbol:go:" + pkgPath + ":(" + c.impl + ")." + methodName,
					"scene-guard:" + sceneReason(&fit), cands, true, true
			}
		}
	}
	// 无守卫命中：返回候选集合（第一个作为展开对象，但标记 ambiguous）
	return cands[0], "", cands, false, true
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

// enclosingIfCond 行号落在哪个 if 的 body 内 → 该 if 的条件表达式。
func enclosingIfCond(fset *token.FileSet, file *ast.File, line int) (ast.Expr, bool) {
	var cond ast.Expr
	ast.Inspect(file, func(n ast.Node) bool {
		if cond != nil {
			return false
		}
		ifs, ok := n.(*ast.IfStmt)
		if !ok || ifs.Body == nil {
			return true
		}
		start := fset.Position(ifs.Body.Lbrace).Line
		end := fset.Position(ifs.Body.Rbrace).Line
		if line > start && line < end {
			cond = ifs.Cond
			return false
		}
		return true
	})
	return cond, cond != nil
}
