package action

import (
	"go/ast"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// seqExternalSamePkgCall 外部包源码内部的**同包函数**调用（裸标识符）：
// 当前分析上下文本身在外部包（`curFile` 以 `<external>/` 开头）时，按
// `req.curSym` 的包路径定位同包声明展开（T10：返回闭包/装饰器内部调用）。
func seqExternalSamePkgCall(a *Actions, req CodeSequenceRequest, fun ast.Expr) (string, string, string, bool) {
	if !strings.HasPrefix(req.curFile, "<external>/") {
		return "", "", "", false
	}
	id, ok := fun.(*ast.Ident)
	if !ok || id.Obj == nil || id.Obj.Kind != ast.Fun {
		return "", "", "", false
	}
	pkg := pkgOfID(domain.CanonicalID(req.curSym))
	if pkg == "" {
		return "", "", "", false
	}
	return pkg, id.Name, "symbol:go:" + pkg + ":" + id.Name, true
}

// seqExternalReturnValues 外部包函数调用 → return 表达式的函数值（闭包/
// 具名回调）。T10：返回闭包按**创建处**外部包文件上下文传递。
func seqExternalReturnValues(a *Actions, req CodeSequenceRequest, ctx *seqFileCtx,
	call *ast.CallExpr, depth int) ([]seqValue, bool) {
	if call == nil || ctx == nil || ctx.file == nil || depth >= seqMaxFactoryDepth {
		return nil, false
	}
	importPath, declName, extID, ok := seqExternalCallTarget(ctx.file, call.Fun)
	if !ok {
		importPath, declName, extID, ok = seqExternalSamePkgCall(a, req, call.Fun)
	}
	if !ok || req.pkgSet[importPath] {
		return nil, false
	}
	if err := req.ctx().Err(); err != nil {
		req.acc.cancel = true
		return nil, false
	}
	pkg := seqExternalPackageFrom(a, req, importPath, seqExternalWorkDir(req))
	if pkg == nil || pkg.err != nil || pkg.std {
		return nil, false
	}
	extCtx, fd := seqFindExternalDecl(pkg, declName)
	if extCtx == nil || fd == nil || fd.Body == nil {
		return nil, false
	}
	sub := req
	sub.env = newSeqEnv(nil)
	bindSeqParams(sub.env, fd.Type.Params, captureSeqClosureArgs(a, req, ctx, call))
	sub.curSym = extID
	sub.curCtx = extCtx
	sub.curFile = extCtx.rel
	sub.curTgts = &seqTargets{}
	sub.sceneConsts = nil
	args := seqCalleeArgLiterals(fd, call, nil)
	var vals []seqValue
	for _, site := range seqReturnSitesOf(fd) {
		if !seqReturnGuardsAllow(site.guards, args, nil) {
			continue
		}
		for _, r := range site.exprs {
			vals = append(vals, captureSeqClosureValueDepth(a, sub, extCtx, r, depth+1))
		}
	}
	return vals, len(vals) > 0
}
