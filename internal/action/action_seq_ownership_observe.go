package action

// 只读参数调用证明（从 action_seq_ownership.go 拆出）。

import (
	"go/ast"
	"go/token"
)

// seqReadOnlyObserveCall 已证明只读的参数调用（函数/方法体只读字段、不保留
// 对象、不把对象交给其它调用）。
func seqReadOnlyObserveCall(req CodeSequenceRequest, call *ast.CallExpr) bool {
	if call == nil || req.curCtx == nil {
		return false
	}
	fn, ok := seqCalleeDecl(req, call)
	if !ok || fn == nil || fn.Body == nil {
		return false
	}
	params := map[string]bool{}
	for _, f := range fn.Type.Params.List {
		for _, n := range f.Names {
			params[n.Name] = true
		}
	}
	if fn.Recv != nil {
		for _, f := range fn.Recv.List {
			for _, n := range f.Names {
				params[n.Name] = true
			}
		}
	}
	if len(params) == 0 {
		return false
	}
	bad := false
	readField := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if bad {
			return false
		}
		switch v := n.(type) {
		case *ast.FuncLit:
			bad = true
			return false
		case *ast.AssignStmt:
			for i, lhs := range v.Lhs {
				if seqExprRootsAny(lhs, params) {
					bad = true
					continue
				}
				// 把参数对象存到全局/字段/容器（非参数目标）→ 被保留。
				if len(v.Lhs) == len(v.Rhs) && i < len(v.Rhs) &&
					seqExprRootsAny(v.Rhs[i], params) {
					bad = true
				}
			}
		case *ast.IncDecStmt:
			if seqExprRootsAny(v.X, params) {
				bad = true
			}
		case *ast.SelectorExpr:
			// 至少读取一次参数对象的字段，才算"观察型只读调用"；空方法体/只
			// 转手传参不构成读证明（未知调用仍按传出处理）。
			if id, ok := runtimeUnparen(v.X).(*ast.Ident); ok && params[id.Name] {
				readField = true
			}
		case *ast.ReturnStmt:
			for _, r := range v.Results {
				if id, ok := runtimeUnparen(r).(*ast.Ident); ok && params[id.Name] {
					bad = true
				}
			}
		case *ast.CallExpr:
			// 实参里直接出现参数对象（不是字段读取）→ 可能被保留。
			for _, a := range v.Args {
				if id, ok := runtimeUnparen(a).(*ast.Ident); ok && params[id.Name] {
					bad = true
				}
				if u, ok := runtimeUnparen(a).(*ast.UnaryExpr); ok && u.Op == token.AND {
					if id, ok := runtimeUnparen(u.X).(*ast.Ident); ok && params[id.Name] {
						bad = true
					}
				}
			}
		}
		return true
	})
	return !bad && readField
}

// seqExprRootsAny 表达式根（含字段读取的基）是否命中参数对象。
func seqExprRootsAny(e ast.Expr, params map[string]bool) bool {
	if e == nil {
		return false
	}
	var hit bool
	ast.Inspect(e, func(n ast.Node) bool {
		if hit {
			return false
		}
		if id, ok := n.(*ast.Ident); ok && params[id.Name] {
			hit = true
			return false
		}
		return true
	})
	return hit
}

// seqCalleeDecl 调用的本地函数/方法声明（外部/多候选 → false）。
func seqCalleeDecl(req CodeSequenceRequest, call *ast.CallExpr) (*ast.FuncDecl, bool) {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		if fun.Obj != nil && fun.Obj.Kind != ast.Fun {
			return nil, false
		}
		decl, count := findFuncDeclInFile(req.curCtx.file, fun.Name)
		if count != 1 {
			return nil, false
		}
		return decl, true
	case *ast.SelectorExpr:
		// 包限定函数 `observer.Observe(...)`：按导入路径解析准确声明。
		if pkgID, ok := fun.X.(*ast.Ident); ok && pkgID.Obj == nil {
			if importPath := seqImportPath(req.curCtx.file, pkgID.Name); importPath != "" {
				// 只解析**仓库内**包：外部依赖不加载源码（T10 深度=1 无外部加载）。
				if _, local := seqRepoPkgDir(req, importPath); local {
					if fn := seqPkgFuncDecl(req, importPath, fun.Sel.Name); fn != nil {
						return fn, true
					}
				}
			}
		}
		base := seqFieldBaseOrigin(req, fun.X)
		if base == nil {
			return nil, false
		}
		fn, _, ok := seqMethodDeclOf(req, base.ref, fun.Sel.Name)
		if !ok {
			return nil, false
		}
		return fn, true
	}
	return nil, false
}

// seqPkgFuncDecl 外部包里的函数声明（唯一同名；含 build 约束过滤）。
func seqPkgFuncDecl(req CodeSequenceRequest, importPath, name string) *ast.FuncDecl {
	var found *ast.FuncDecl
	count := 0
	for _, ctx := range seqPkgFilesForReq(req, importPath, "") {
		if ctx == nil || ctx.file == nil || seqFileHasBuildConstraint(ctx) {
			continue
		}
		for _, d := range ctx.file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || fd.Name == nil || fd.Name.Name != name {
				continue
			}
			found = fd
			count++
		}
	}
	if count != 1 {
		return nil
	}
	return found
}
