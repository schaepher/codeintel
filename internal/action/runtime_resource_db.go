package action

// U04d：包级 DB 句柄身份与写入资源观察（从 runtime_resource.go 拆出）。

import (
	"go/ast"
	"go/token"
)

// sqlResourceSymbols 该表达式的准确包级 DB 句柄符号（U04d）。
// 支持同文件/跨文件对象（parser 跨文件引用时 Obj 为 nil）。
func (e *rtEval) sqlResourceSymbols(frame *rtFrame, expr ast.Expr) (string, bool) {
	id, ok := expr.(*ast.Ident)
	if !ok || id.Name == "" {
		return "", false
	}
	if id.Obj != nil {
		if !e.an.pkgVars[id.Obj] {
			return "", false
		}
		if !runtimeIsSQLDBDecl(id, frame.ctx.file) {
			return "", false
		}
	} else {
		// 跨文件引用：按包级声明名核对 `var X *sql.DB`。
		if !e.an.pkgVarNames[id.Name] || !e.sqlDBDeclByName(frame, id.Name) {
			return "", false
		}
	}
	return "symbol:go:" + frame.pkgPath + ":" + id.Name, true
}

// sqlDBDeclByName 包级 `var X *sql.DB`（无初始化）声明核对（跨文件）。
func (e *rtEval) sqlDBDeclByName(frame *rtFrame, name string) bool {
	rel := ""
	if frame != nil && frame.ctx != nil {
		rel = frame.ctx.rel
	}
	for _, file := range seqPackageFilesOf(e.req, rel) {
		ctx := seqFileCtxFor(e.req, file)
		if ctx == nil || ctx.file == nil {
			continue
		}
		for _, decl := range ctx.file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || len(vs.Values) > 0 || vs.Type == nil {
					continue
				}
				for _, n := range vs.Names {
					if n.Name == name && runtimeIsSQLDBTypeExpr(vs.Type, ctx.file) {
						return true
					}
				}
			}
		}
	}
	return false
}

// observeWriteResource 已证明的 DB 写入调用 → 记录资源绑定结论。
// 返回 (是否写入调用, 求值后的状态)；实参副作用在这里先求值（U04d）。
func (e *rtEval) observeWriteResource(frame *rtFrame, call *ast.CallExpr, s *rtState,
	depth int) (bool, []*rtState) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false, nil
	}
	if sel.Sel.Name != "Exec" && sel.Sel.Name != "ExecContext" {
		return false, nil
	}
	sym, ok := e.sqlResourceSymbols(frame, sel.X)
	if !ok {
		return false, nil
	}
	// 实参副作用（ResetArg 等）先求值，再检查绑定。
	_, states := e.evalArgs(frame, call.Args, s, depth)
	if len(states) == 0 {
		states = []*rtState{s}
	}
	g := e.an.globalByNameOf(&ast.Ident{Name: nameOfSymbol(sym)})
	res := &SeqStorageResource{Symbol: sym, SnapshotHash: e.an.snap.Hash}
	switch {
	case g.mutated:
		res.Status = SeqResourceMutated
	default:
		if source := runtimeResolveSource(e.an.snap, sym); source != "" {
			res.Status = SeqResourceBound
			res.SourceID = source
		} else {
			res.Status = SeqResourceBindingMissing
		}
	}
	e.an.writeRes[call] = append(e.an.writeRes[call], res)
	return true, states
}

// recordReadResource 读取调用的资源绑定结论（U04d 多上下文聚合）。
func (e *rtEval) recordReadResource(call *ast.CallExpr, status, sym, source string) {
	if e.an == nil || call == nil {
		return
	}
	res := &SeqStorageResource{Status: status, Symbol: sym, SnapshotHash: e.an.snap.Hash}
	if status == SeqResourceBound {
		res.SourceID = source
	}
	e.an.writeRes[call] = append(e.an.writeRes[call], res)
}

// nameOfSymbol `symbol:go:pkg:DB` → `DB`。
func nameOfSymbol(sym string) string {
	for i := len(sym) - 1; i >= 0; i-- {
		if sym[i] == ':' {
			return sym[i+1:]
		}
	}
	return sym
}
