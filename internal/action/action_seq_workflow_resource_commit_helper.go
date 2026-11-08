package action

// B06e.4：helper 返回槽的事务来源解析（局部变量/命名返回值/跨文件/跨包）。

import (
	"go/ast"
	"path/filepath"
)

// txCallOrigin 调用表达式槽 0 的事务来源键（直接 Begin / 同包或跨包 helper
// 的返回槽；冲突返回、返回其它事务或外部源码缺失 → 空，不猜）。
func (a *seqCommitAnalyzer) txCallOrigin(call *ast.CallExpr, depth int) string {
	if call == nil || depth > seqCommitHelperMaxDepth {
		return ""
	}
	if seqIsBeginCall(call) {
		return a.key(a.offset(call), a.rel)
	}
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		fd, file := seqPkgFuncUniqueIn(a.files, fun.Name)
		if fd == nil || file == nil || file.fset == nil {
			return ""
		}
		return a.helperTxReturnOrigin(fd, file, a.files, a.dir, depth)
	case *ast.SelectorExpr:
		// B06e.4：跨包 helper（`pkg.OpenTx(ctx)`）按导入路径定位真实声明。
		if a.file == nil || a.file.file == nil || a.repo == "" {
			return ""
		}
		importPath := callImportPath(a.file.file, fun)
		dir, ok := seqRepoPkgDirFor(a.repo, importPath)
		if !ok {
			return ""
		}
		pkgDir := filepath.Join(a.repo, dir)
		files := seqParsePackageDir(pkgDir)
		fd, file := seqPkgFuncUniqueIn(files, fun.Sel.Name)
		if fd == nil || file == nil || file.fset == nil {
			return ""
		}
		return a.helperTxReturnOrigin(fd, file, files, dir, depth)
	}
	return ""
}

// helperTxReturnOrigin helper 的槽 0 返回来源：所有 return 必须一致地解析到
// 同一个 Begin 调用（局部变量/命名返回值/跨文件/跨包；冲突返回不证明）。
func (a *seqCommitAnalyzer) helperTxReturnOrigin(fd *ast.FuncDecl, file *seqParsedGoFile,
	files map[string]*seqParsedGoFile, pkgDir string, depth int) string {
	if fd == nil || fd.Body == nil || file == nil || file.fset == nil ||
		depth >= seqCommitHelperMaxDepth {
		return ""
	}
	sub := &seqCommitAnalyzer{fset: file.fset, rel: file.rel, dir: pkgDir,
		repo: a.repo, files: files, file: file, fn: fd}
	sub.walkStmts(fd.Body.List, newSeqCommitState())
	if len(sub.returns) == 0 {
		return ""
	}
	origin := ""
	for _, ret := range sub.returns {
		cur := sub.returnSlotOrigin(ret, 0, depth+1)
		if cur == "" {
			return ""
		}
		if origin != "" && origin != cur {
			return "" // 冲突返回：不同 Begin 调用，不能互相证明。
		}
		origin = cur
	}
	return origin
}

// returnSlotOrigin 单个 return 的槽来源（`return tx, err` / 命名返回 `return`）。
func (a *seqCommitAnalyzer) returnSlotOrigin(ret seqCommitReturn, slot, depth int) string {
	if ret.ret == nil || ret.st == nil {
		return ""
	}
	if slot < len(ret.ret.Results) {
		return a.exprTxOrigin(ret.ret.Results[slot], ret.st, depth)
	}
	if len(ret.ret.Results) != 0 {
		return ""
	}
	obj := seqNamedResultObj(a.fn, slot)
	if obj == nil {
		return ""
	}
	return ret.st.binds[obj]
}

// exprTxOrigin 表达式在当前状态里的事务来源（变量绑定 / Begin / helper 调用）。
func (a *seqCommitAnalyzer) exprTxOrigin(e ast.Expr, st *seqCommitState, depth int) string {
	switch v := runtimeUnparen(e).(type) {
	case *ast.Ident:
		if v.Obj != nil {
			return st.binds[v.Obj]
		}
	case *ast.CallExpr:
		return a.txCallOrigin(v, depth)
	}
	return ""
}

// seqNamedResultObj 第 slot 个命名返回值对象（未命名/越界 → nil）。
func seqNamedResultObj(fd *ast.FuncDecl, slot int) *ast.Object {
	if fd == nil || fd.Type == nil || fd.Type.Results == nil {
		return nil
	}
	idx := 0
	for _, field := range fd.Type.Results.List {
		for _, name := range field.Names {
			if idx == slot {
				return name.Obj
			}
			idx++
		}
		if len(field.Names) == 0 {
			idx++ // 未命名返回值也占一个槽位。
		}
	}
	return nil
}

// seqPkgFuncUniqueIn 包内唯一同名顶层函数（重名 → nil）。
func seqPkgFuncUniqueIn(files map[string]*seqParsedGoFile, name string) (*ast.FuncDecl, *seqParsedGoFile) {
	var found *ast.FuncDecl
	var file *seqParsedGoFile
	for _, f := range files {
		for _, d := range f.file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || fd.Name == nil || fd.Name.Name != name {
				continue
			}
			if found != nil {
				return nil, nil
			}
			found, file = fd, f
		}
	}
	return found, file
}
