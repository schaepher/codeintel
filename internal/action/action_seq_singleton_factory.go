package action

// U07：单例 getter + 条件工厂（switch 选实现）的来源与守卫。
//
// 形态：`var selected API`；`func Get() API { return selected }`；
// `func initialize(){ selected = Choose(Handler{ID:&IDHandler{}, MY:&MYHandler{}}).(API) }`；
// `func Choose(h Handler) interface{} { switch region() { case "ID": v = h.ID } ... return v }`
//
// 只按源码证明：工厂 return/赋值路径的守卫来自 switch/if 条件，字段值来自
// 调用点的复合字面量；无法证明的来源不进入候选。

import (
	"go/ast"

	"github.com/schaepher/codeintel/internal/domain"
)

// seqGetterReturnedVar getter 唯一 return 的包级变量名（跨文件对象也可）。
func seqGetterReturnedVar(a *Actions, req CodeSequenceRequest, fn *ast.FuncDecl) (string, bool) {
	if fn == nil || fn.Body == nil || len(fn.Body.List) != 1 {
		return "", false
	}
	ret, ok := fn.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return "", false
	}
	id, ok := ret.Results[0].(*ast.Ident)
	if !ok || id.Name == "" {
		return "", false
	}
	return id.Name, true
}

// seqWalkVarAssigns 遍历函数体，收集对某包级变量的赋值（携带 switch/if 守卫）。
func seqWalkVarAssigns(n ast.Node, guard, name string, fn func(ast.Expr, string)) {
	seqWalkGuarded(n, guard, func(nd ast.Node, g string) bool {
		as, ok := nd.(*ast.AssignStmt)
		if !ok {
			return true
		}
		if len(as.Lhs) != len(as.Rhs) {
			return true
		}
		for i, lhs := range as.Lhs {
			if id, ok := lhs.(*ast.Ident); ok && id.Name == name {
				fn(as.Rhs[i], g)
			}
		}
		return true
	})
}

// seqWalkGuarded 通用带守卫遍历（if/switch case 携带条件文本）。
func seqWalkGuarded(n ast.Node, guard string, fn func(ast.Node, string) bool) {
	switch x := n.(type) {
	case nil:
		return
	case *ast.BlockStmt:
		for _, st := range x.List {
			seqWalkGuarded(st, guard, fn)
		}
	case *ast.IfStmt:
		if x.Init != nil {
			seqWalkGuarded(x.Init, guard, fn)
		}
		g := seqGuardAnd(guard, seqNodeText(x.Cond))
		seqWalkGuarded(x.Body, g, fn)
		if x.Else != nil {
			seqWalkGuarded(x.Else, seqGuardAnd(guard, "!("+seqNodeText(x.Cond)+")"), fn)
		}
	case *ast.SwitchStmt:
		if x.Init != nil {
			seqWalkGuarded(x.Init, guard, fn)
		}
		tag := seqNodeText(x.Tag)
		for _, c := range x.Body.List {
			cc, ok := c.(*ast.CaseClause)
			if !ok {
				continue
			}
			g := seqGuardAnd(guard, seqCaseGuard(tag, cc.List))
			for _, st := range cc.Body {
				seqWalkGuarded(st, g, fn)
			}
		}
	case *ast.ForStmt:
		seqWalkGuarded(x.Body, guard, fn)
	case *ast.RangeStmt:
		seqWalkGuarded(x.Body, guard, fn)
	case *ast.LabeledStmt:
		seqWalkGuarded(x.Stmt, guard, fn)
	case *ast.FuncLit:
		// 闭包内的写入属于"潜在写者"：只作为守卫未知的来源处理由调用方决定。
		seqWalkGuarded(x.Body, guard, fn)
	default:
		if !fn(n, guard) {
			return
		}
	}
}

// seqSingletonCandidates 单例 getter → 工厂 switch 选实现的候选（含守卫）。
func seqSingletonCandidates(a *Actions, req CodeSequenceRequest, f *ast.File, inner *ast.CallExpr,
	curPkg string) ([]seqFieldCandidate, string, bool) {
	target, ok := seqASTCallTarget(inner.Fun, f, curPkg)
	if !ok {
		return nil, "", false
	}
	ctx, fn, ok := loadSeqFuncDecl(a, req, target)
	if !ok || ctx == nil || fn == nil {
		return nil, "", false
	}
	varName, ok := seqGetterReturnedVar(a, req, fn)
	if !ok {
		return nil, "", false
	}
	var cands []seqFieldCandidate
	factoryID := ""
	ownerPkg := pkgOfID(domain.CanonicalID(target))
	for _, rel := range seqPackageFilesOf(req, ctx.rel) {
		fctx := seqFileCtxFor(req, rel)
		if fctx == nil || fctx.file == nil {
			continue
		}
		for _, d := range fctx.file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil || fd.Name == nil || fd.Name.Name == fn.Name.Name {
				continue
			}
			seqWalkVarAssigns(fd.Body, "", varName, func(rhs ast.Expr, guard string) {
				expr := runtimeUnparen(rhs)
				if ta, ok := expr.(*ast.TypeAssertExpr); ok {
					expr = runtimeUnparen(ta.X)
				}
				if call, ok := expr.(*ast.CallExpr); ok {
					if srcs, fsym, ok2 := seqFactoryArgFieldSources(a, req, fctx, call, ownerPkg); ok2 {
						for _, s := range srcs {
							cands = append(cands, seqFieldCandidate{pkgPath: s.pkgPath,
								typeName: s.typeName, guard: seqGuardAnd(guard, s.guard)})
						}
						if fsym != "" {
							factoryID = fsym
						}
						return
					}
				}
				for _, s := range rhsTypes(a, req, fctx.file, importAliases(fctx.file), expr, 3, ownerPkg) {
					if s.typeName == "" || s.pkgPath == "" {
						continue
					}
					cands = append(cands, seqFieldCandidate{pkgPath: s.pkgPath, typeName: s.typeName, guard: guard})
				}
			})
		}
	}
	cands = appendUniqueFieldCandidates(cands)
	if len(cands) == 0 {
		return nil, "", false
	}
	return cands, factoryID, true
}
