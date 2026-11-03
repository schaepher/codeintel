package action

// U07：条件工厂（switch 选实现）的返回槽字段来源解析。

import (
	"go/ast"

	"github.com/schaepher/codeintel/internal/domain"
)

// seqFactoryArgFieldSources 工厂调用 → 返回槽的字段来源（守卫 + 调用点实参
// 复合字面量的字段具体类型）。只处理 `h.Field` 形态。
func seqFactoryArgFieldSources(a *Actions, req CodeSequenceRequest, fctx *seqFileCtx,
	call *ast.CallExpr, curPkg string) ([]seqFieldCandidate, string, bool) {
	target, ok := seqASTCallTarget(call.Fun, fctx.file, curPkg)
	if !ok {
		return nil, "", false
	}
	ctx, fn, ok := loadSeqFuncDecl(a, req, target)
	if !ok || fn == nil || ctx == nil || fn.Type.Params == nil {
		return nil, "", false
	}
	// 形参名 → 调用点复合字面量（只支持结构体字面量实参）。
	argLits := map[string]*ast.CompositeLit{}
	idx := 0
	for _, field := range fn.Type.Params.List {
		for _, name := range field.Names {
			if idx < len(call.Args) {
				if cl, ok := runtimeUnparen(call.Args[idx]).(*ast.CompositeLit); ok {
					argLits[name.Name] = cl
				}
			}
			idx++
		}
	}
	if len(argLits) == 0 {
		return nil, "", false
	}
	var out []seqFieldCandidate
	collect := func(e ast.Expr, guard string) {
		guard = seqResolveGuardLocalTag(guard, fn)
		sel, ok := runtimeUnparen(e).(*ast.SelectorExpr)
		if !ok {
			return
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok {
			return
		}
		cl := argLits[id.Name]
		if cl == nil {
			return
		}
		for _, el := range cl.Elts {
			kv, ok := el.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok || key.Name != sel.Sel.Name {
				continue
			}
			for _, s := range rhsTypes(a, req, ctx.file, importAliases(ctx.file), kv.Value, 3,
				pkgOfID(domain.CanonicalID(target))) {
				if s.typeName == "" || s.pkgPath == "" {
					continue
				}
				out = append(out, seqFieldCandidate{pkgPath: s.pkgPath, typeName: s.typeName, guard: guard})
			}
		}
	}
	// 返回表达式直接是 `h.Field`。
	for _, site := range seqReturnSitesOf(fn) {
		g := seqGuardText(site.guards)
		for _, r := range site.exprs {
			collect(r, g)
		}
	}
	// 局部变量转发：`v = h.Field`（带守卫）后 `return v`。
	for _, site := range seqReturnSitesOf(fn) {
		for _, r := range site.exprs {
			id, ok := r.(*ast.Ident)
			if !ok {
				continue
			}
			seqWalkVarAssigns(fn.Body, "", id.Name, func(rhs ast.Expr, guard string) {
				collect(rhs, guard)
			})
		}
	}
	out = appendUniqueFieldCandidates(out)
	if len(out) == 0 {
		return nil, "", false
	}
	return out, target, true
}

// seqGuardText 返回路径守卫 → 文本（与工厂分支标签一致）。
func seqGuardText(guards []seqReturnGuard) string {
	g := ""
	for _, guard := range guards {
		part := seqNodeText(guard.cond)
		if guard.field != "" && guard.lit != "" {
			part = guard.field + " == " + guard.lit
		}
		if part == "" {
			continue
		}
		if !guard.want {
			part = "!(" + part + ")"
		}
		g = seqGuardAnd(g, part)
	}
	return g
}
