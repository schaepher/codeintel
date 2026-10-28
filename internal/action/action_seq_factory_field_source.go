package action

// todo25 §3.4：工厂字段续接的源码读取/守卫遍历辅助（主流程见
// action_seq_factory_field.go）。

import (
	"go/ast"
	"go/token"

	"github.com/schaepher/codeintel/internal/domain"
)

// seqSwitchHasDefault switch 是否有 default 分支（无 default = 存在未覆盖路径）。
func seqSwitchHasDefault(body *ast.BlockStmt) bool {
	if body == nil {
		return false
	}
	for _, st := range body.List {
		cc, ok := st.(*ast.CaseClause)
		if ok && cc.List == nil {
			return true
		}
	}
	return false
}

// seqMethodReturnField 方法体 return <receiver>.<field>（唯一字段）。
func seqMethodReturnField(a *Actions, req CodeSequenceRequest, symID, recvType string) (string, bool) {
	n, err := a.repo.GetSymbol(domain.CanonicalID(symID))
	if err != nil || n.FilePath == "" {
		return "", false
	}
	ctx := seqFileCtxFor(req, n.FilePath)
	if ctx == nil {
		return "", false
	}
	fn := funcDeclAtLine(ctx.fset, ctx.file, n.LineStart)
	if fn == nil || fn.Recv == nil || len(fn.Recv.List) == 0 {
		return "", false
	}
	recvName := ""
	if names := fn.Recv.List[0].Names; len(names) > 0 {
		recvName = names[0].Name
	}
	field := ""
	ok := true
	ast.Inspect(fn.Body, func(nd ast.Node) bool {
		ret, isRet := nd.(*ast.ReturnStmt)
		if !isRet {
			return true
		}
		for _, e := range ret.Results {
			sel, isSel := e.(*ast.SelectorExpr)
			if !isSel {
				ok = false
				return false
			}
			id, isID := sel.X.(*ast.Ident)
			if !isID || id.Name != recvName || (field != "" && field != sel.Sel.Name) {
				ok = false
				return false
			}
			field = sel.Sel.Name
		}
		return true
	})
	if !ok || field == "" {
		return "", false
	}
	return field, true
}

// seqFactoryFieldCandidates 工厂函数内该字段 setter 的实参类型 + 守卫。
func seqFactoryFieldCandidates(a *Actions, req CodeSequenceRequest, factoryID, recvType,
	field, curPkg string) ([]seqFieldCandidate, bool) {
	fn, ctx, ok := seqFuncSource(a, req, factoryID)
	if !ok {
		return nil, false
	}
	returned := seqReturnedIdents(fn)
	if len(returned) == 0 || seqFactoryFallthrough(fn, returned) {
		return nil, false
	}
	// 字段 writer 可能在嵌入基类上（提升方法）：沿嵌入链找唯一 owner；
	// 多条提升路径/找不到 → 保守放弃（不挑第一个）。
	ownerTypes := seqFieldOwnerTypes(a, req, ctx.rel, recvType, field)
	if len(ownerTypes) != 1 {
		return nil, false
	}
	paramIdx := seqFieldWriterParams(a, req, ownerTypes[0], field)
	if len(paramIdx) == 0 {
		return nil, false
	}
	var out []seqFieldCandidate
	unsafe := false
	imports := importAliases(ctx.file)
	resolve := func(e ast.Expr) []writeSource {
		return rhsTypes(a, req, ctx.file, imports, e, 3, pkgOfID(domain.CanonicalID(factoryID)))
	}
	seqFactoryCallsWithClosures(fn, resolve, func(call *ast.CallExpr, guard string,
		subst map[*ast.Object][]writeSource) {
		if seqFactoryUnknownMethod(call, returned, paramIdx) {
			// todo25 §3.4：非已知 writer 的 receiver 方法/实例逃逸可能改写
			// 字段——不能假设"没看到写入就没有写入"。
			unsafe = true
			return
		}
		sel, isSel := call.Fun.(*ast.SelectorExpr)
		if !isSel {
			return
		}
		idx, hit := paramIdx[sel.Sel.Name]
		if !hit || idx < 0 || idx >= len(call.Args) {
			return
		}
		id, isID := sel.X.(*ast.Ident)
		if !isID || id.Obj == nil || !returned[id.Obj] {
			return
		}
		var types []writeSource
		if argID, ok := call.Args[idx].(*ast.Ident); ok && argID.Obj != nil {
			types = subst[argID.Obj]
		}
		if len(types) == 0 {
			types = resolve(call.Args[idx])
		}
		if len(types) == 0 {
			return
		}
		out = append(out, seqFieldCandidate{pkgPath: types[0].pkgPath,
			typeName: types[0].typeName, guard: guard})
	})
	if unsafe {
		return nil, false
	}
	out = appendUniqueFieldCandidates(out)
	return out, len(out) > 0
}

// seqFieldWriterParams 字段 writer 方法 → (方法名 → 被写实参下标)。
func seqFieldWriterParams(a *Actions, req CodeSequenceRequest, recvType, field string) map[string]int {
	key := "." + recvType + "." + field
	writers, err := a.repo.GetFieldWriters(key)
	if err != nil || len(writers) == 0 {
		writers, _ = a.repo.GetFieldWritersBySuffix(key)
	}
	out := map[string]int{}
	for _, w := range writers {
		fn, _, ok := seqFuncSource(a, req, w)
		if !ok || fn.Recv == nil {
			continue
		}
		idx := seqAssignedParamIndex(fn, field)
		if idx < 0 {
			continue
		}
		out[fn.Name.Name] = idx
	}
	return out
}

// seqAssignedParamIndex 方法体 `x.field = <param>` 的形参下标（普通参数）。
func seqAssignedParamIndex(fn *ast.FuncDecl, field string) int {
	names := map[string]int{}
	i := 0
	for _, fl := range fn.Type.Params.List {
		if len(fl.Names) == 0 {
			continue
		}
		for _, nm := range fl.Names {
			names[nm.Name] = i
			i++
		}
	}
	idx := -1
	ast.Inspect(fn.Body, func(nd ast.Node) bool {
		as, ok := nd.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		sel, ok := as.Lhs[0].(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != field {
			return true
		}
		id, ok := as.Rhs[0].(*ast.Ident)
		if ok {
			if p, hit := names[id.Name]; hit {
				idx = p
			}
		}
		return false
	})
	return idx
}

// seqFuncSource 符号 → (FuncDecl, 文件上下文)。
func seqFuncSource(a *Actions, req CodeSequenceRequest, symID string) (*ast.FuncDecl, *seqFileCtx, bool) {
	n, err := a.repo.GetSymbol(domain.CanonicalID(symID))
	if err != nil || n.FilePath == "" {
		return nil, nil, false
	}
	ctx := seqFileCtxFor(req, n.FilePath)
	if ctx == nil {
		return nil, nil, false
	}
	fn := funcDeclAtLine(ctx.fset, ctx.file, n.LineStart)
	return fn, ctx, fn != nil
}

// funcDeclAtLine 定位包含该行号的函数声明。
func funcDeclAtLine(fset *token.FileSet, f *ast.File, line int) *ast.FuncDecl {
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		if line >= fset.Position(fd.Pos()).Line && line <= fset.Position(fd.End()).Line {
			return fd
		}
	}
	return nil
}

// seqReturnedIdents 工厂返回语句里的局部变量**声明对象**（返回实例候选；
// 按对象身份而不是同名标识符，避免同名阴影/另一个实例借用）。
func seqReturnedIdents(fn *ast.FuncDecl) map[*ast.Object]bool {
	out := map[*ast.Object]bool{}
	ast.Inspect(fn.Body, func(nd ast.Node) bool {
		ret, ok := nd.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		for _, e := range ret.Results {
			if id, ok := e.(*ast.Ident); ok && id.Obj != nil {
				out[id.Obj] = true
			}
		}
		return true
	})
	return out
}

// seqFactoryFallthrough 工厂里出现 fallthrough/别名/取址逃逸时保守放弃
// （无法证明每个候选的实际执行条件）。
func seqFactoryFallthrough(fn *ast.FuncDecl, returned map[*ast.Object]bool) bool {
	bad := false
	ast.Inspect(fn.Body, func(nd ast.Node) bool {
		switch x := nd.(type) {
		case *ast.SwitchStmt:
			// T05：switch 没有 default → 存在"没走任何 setter"的路径，
			// 字段写入证明不完整（不能假设 return 前一定被赋值）。
			if !seqSwitchHasDefault(x.Body) {
				bad = true
			}
		case *ast.TypeSwitchStmt:
			if !seqSwitchHasDefault(x.Body) {
				bad = true
			}
		case *ast.BranchStmt:
			if x.Tok == token.FALLTHROUGH {
				bad = true
			}
		case *ast.UnaryExpr:
			if x.Op == token.AND {
				if id, ok := x.X.(*ast.Ident); ok && returned[id.Obj] {
					bad = true
				}
			}
		case *ast.AssignStmt:
			if len(x.Lhs) == 1 && len(x.Rhs) == 1 {
				if id, ok := x.Rhs[0].(*ast.Ident); ok && returned[id.Obj] {
					if lhs, ok := x.Lhs[0].(*ast.Ident); ok && lhs.Obj != id.Obj {
						bad = true
					}
				}
			}
		}
		return true
	})
	return bad
}
