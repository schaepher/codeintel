package action

// U04y：调用构造实例的字段读写根 + 别名初始化类型解析。

import (
	"go/ast"
	"go/token"
)

// seqIdentStructType 标识符声明类型 → 结构体类型引用（票根为参数/局部变量）。
func seqIdentStructType(req CodeSequenceRequest, id *ast.Ident) (seqORMTypeRef, bool) {
	return seqIdentStructTypeDepth(req, id, 0)
}

func seqIdentStructTypeDepth(req CodeSequenceRequest, id *ast.Ident, depth int) (seqORMTypeRef, bool) {
	if id == nil || id.Obj == nil || req.curCtx == nil || req.curCtx.file == nil ||
		depth > seqScalarOriginMaxDepth {
		return seqORMTypeRef{}, false
	}
	var typ ast.Expr
	var init ast.Expr
	switch d := id.Obj.Decl.(type) {
	case *ast.Field:
		typ = d.Type
	case *ast.ValueSpec:
		typ = d.Type
		for i, name := range d.Names {
			if name.Obj == id.Obj && i < len(d.Values) {
				init = d.Values[i]
			}
		}
	case *ast.AssignStmt:
		if len(d.Lhs) == len(d.Rhs) {
			for i, lhs := range d.Lhs {
				if lid, ok := lhs.(*ast.Ident); ok && lid.Obj == id.Obj && i < len(d.Rhs) {
					init = d.Rhs[i]
				}
			}
		}
	}
	if typ == nil && init != nil {
		// `alias := record`：类型沿初始化来源变量（同一分配身份）解析。
		if src, ok := runtimeUnparen(init).(*ast.Ident); ok {
			return seqIdentStructTypeDepth(req, src, depth+1)
		}
	}
	if typ == nil {
		return seqORMTypeRef{}, false
	}
	c := &seqPersistCtx{file: req.curCtx.file, pkgPath: pkgPathOf(req.curSym),
		src: req.curCtx.src, fset: req.curCtx.fset, repoAbs: req.RepoAbs,
		fileRel: req.curCtx.rel, req: &req}
	ref, ok := seqORMTypeExprRef(c, typ, req.curCtx.file, c.pkgPath, 0)
	if !ok {
		return seqORMTypeRef{}, false
	}
	return ref, true
}

// seqCallCtorFieldBase 调用表达式的构造实例根（`Get().ID = ...` / 字段读取）：
// 只接受单一 return 且返回 `new(T)` / `&T{}` / `T{}` 的函数/方法。
func seqCallCtorFieldBase(req CodeSequenceRequest, call *ast.CallExpr) *seqFieldBase {
	if call == nil || req.curCtx == nil || req.curCtx.file == nil {
		return nil
	}
	var fd *ast.FuncDecl
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		if fun.Obj != nil && fun.Obj.Kind != ast.Fun {
			return nil
		}
		decl, count := findFuncDeclInFile(req.curCtx.file, fun.Name)
		if count != 1 {
			return nil
		}
		fd = decl
	case *ast.SelectorExpr:
		base := seqFieldBaseOrigin(req, fun.X)
		if base == nil {
			return nil
		}
		decl, _, ok := seqMethodDeclOf(req, base.ref, fun.Sel.Name)
		if !ok {
			return nil
		}
		fd = decl
	default:
		return nil
	}
	if fd == nil || fd.Body == nil || len(fd.Body.List) != 1 {
		return nil
	}
	ret, ok := fd.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return nil
	}
	e := runtimeUnparen(ret.Results[0])
	if u, ok := e.(*ast.UnaryExpr); ok && u.Op == token.AND {
		e = runtimeUnparen(u.X)
	}
	var typ ast.Expr
	switch x := e.(type) {
	case *ast.CompositeLit:
		typ = x.Type
	case *ast.CallExpr:
		id, ok := x.Fun.(*ast.Ident)
		if !ok || id.Name != "new" || id.Obj != nil || len(x.Args) != 1 {
			return nil
		}
		typ = x.Args[0]
	default:
		return nil
	}
	c := &seqPersistCtx{file: req.curCtx.file, pkgPath: pkgPathOf(req.curSym),
		src: req.curCtx.src, fset: req.curCtx.fset, repoAbs: req.RepoAbs,
		fileRel: req.curCtx.rel, req: &req}
	ref, ok := seqORMTypeExprRef(c, typ, req.curCtx.file, c.pkgPath, 0)
	if !ok || ref.name == "" || ref.pkgPath == "" {
		return nil
	}
	return &seqFieldBase{root: &SeqFieldRoot{Kind: "struct_literal",
		TypeID: "symbol:go:" + ref.pkgPath + ":" + ref.name}, ref: ref}
}
