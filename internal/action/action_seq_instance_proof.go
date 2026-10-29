package action

import (
	"go/ast"

	"github.com/schaepher/codeintel/internal/domain"
)

// seqParamInstanceProven 形参的实例来源证明：当前函数**全部已索引调用点**在
// 对应实参位置都给出同一个具体类型来源（字面量/构造器）。跨函数形参绑定
// 是真实来源（如 `Entry(){Run(&Store{})}`），此时可以继续用接口实现候选；
// 无调用点/调用点未知/多来源 → 未证明。
func seqParamInstanceProven(a *Actions, req CodeSequenceRequest, id *ast.Ident) bool {
	if a == nil || a.repo == nil || id == nil || req.curCtx == nil || req.curCtx.file == nil {
		return false
	}
	fn := seqEnclosingFunc(req.curCtx.file, id.Pos())
	if fn == nil {
		return false
	}
	idx := seqFuncParamIndex(fn, id.Name)
	if idx < 0 {
		return false
	}
	callers, err := a.repo.GetCallers(domain.CanonicalID(req.curSym), 1, MinConfidence)
	if err != nil || len(callers) == 0 || len(callers) > 16 {
		return false
	}
	for _, c := range callers {
		line := seqFactLine(c)
		if line <= 0 {
			return false
		}
		caller, cerr := a.repo.GetSymbol(c.SourceID)
		if cerr != nil || caller.FilePath == "" {
			return false
		}
		cc := seqFileCtxFor(req, caller.FilePath)
		if cc == nil {
			return false
		}
		call := seqCallAtLine(cc.file, cc.fset, line)
		if call == nil || len(call.Args) <= idx {
			return false
		}
		srcs := rhsTypes(a, req, cc.file, importAliases(cc.file), call.Args[idx], 3, pkgOfID(caller.ID))
		if hasUnknownWriteSource(srcs) || len(knownWriteSources(srcs)) != 1 {
			return false
		}
	}
	return true
}

// seqReceiverCallUnproven 接收者是**调用结果**（`f(...).M()`）时，被调函数
// 的声明返回类型必须是已索引的具体类型；返回接口 → 实例来源未证明（T04：
// 不能凭"调用边已指向某个具体方法"就把接口返回值当确定实例）。
// 类型未在本仓索引（标准库/外部）→ 不在此判定，保持原行为。
func seqReceiverCallUnproven(a *Actions, req CodeSequenceRequest, fun ast.Expr,
	f *ast.File, curPkg string) (string, bool) {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	call, ok := sel.X.(*ast.CallExpr)
	if !ok || f == nil {
		return "", false
	}
	target, ok := seqASTCallTarget(call.Fun, f, curPkg)
	if !ok {
		return "", false
	}
	ctx, fd, ok := loadSeqFuncDecl(a, req, target)
	if !ok || fd == nil || fd.Type.Results == nil || len(fd.Type.Results.List) == 0 {
		return "", false
	}
	retType := fd.Type.Results.List[0].Type
	for {
		if st, ok := retType.(*ast.StarExpr); ok {
			retType = st.X
			continue
		}
		break
	}
	name := ""
	switch x := retType.(type) {
	case *ast.Ident:
		name = x.Name
	case *ast.SelectorExpr:
		name = x.Sel.Name
	}
	if name == "" {
		return "", false
	}
	pkg := pkgOfID(domain.CanonicalID(target))
	n, err := a.repo.GetSymbol(domain.CanonicalID("symbol:go:" + pkg + ":" + name))
	if err != nil || n == nil || n.Kind != domain.KindInterface {
		return "", false
	}

	srcs := funcDeclReturnTypes(a, req, ctx.file, importAliases(ctx.file), fd, 3, pkg)
	known := knownWriteSources(srcs)
	if len(known) == 1 && !hasUnknownWriteSource(srcs) && !a.isIfaceTypeName(req, known[0].typeName) {
		return "", false
	}
	return "调用 " + seqExprBrief(call) + " 的返回类型 " + name + " 是接口（实例来源未证明，未展开）", true
}
