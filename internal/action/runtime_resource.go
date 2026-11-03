package action

// U04d：写入资源绑定（storage.resource）与读写共享的资源失效。
//
// 只复用运行时快照的通用 v2 binding：**准确包级、无初始化的
// `var X *sql.DB`** 才能绑定；绑定/失效都不证明写入成功。

import (
	"go/ast"
	"go/token"
)

// 资源绑定状态。
const (
	SeqResourceBound          = "bound"
	SeqResourceNotVisited     = "analysis_not_visited"
	SeqResourceBindingMissing = "binding_missing"
	SeqResourceMutated        = "binding_mutated"
	SeqResourceAmbiguous      = "ambiguous_resource"
)

// SeqStorageResource 写入/读取资源绑定证据（U04d）。
type SeqStorageResource struct {
	Status       string `json:"status"`
	SourceID     string `json:"source_id,omitempty"`
	Symbol       string `json:"symbol,omitempty"`
	SnapshotHash string `json:"snapshot_hash,omitempty"`
}

// unknownCallEffects 未解释的调用可能改写任何包级句柄 → 全部绑定失效。
func (e *rtEval) unknownCallEffects(frame *rtFrame, call *ast.CallExpr) {
	if e.an == nil || call == nil {
		return
	}
	if e.runtimePureCall(frame, call) {
		return
	}
	e.an.mutateAllGlobals()
}

// runtimePureCall 已建模为纯值/只读的调用（不失效包级绑定）。
func (e *rtEval) runtimePureCall(frame *rtFrame, call *ast.CallExpr) bool {
	if call == nil {
		return false
	}
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		// 只认**未被遮蔽**的预声明内建（同名包级函数不是内建）。
		if seqRuntimeShadowedBuiltin(e, frame, fun) {
			return false
		}
		return fun.Obj == nil && seqPureBuiltinName(fun.Name)
	case *ast.SelectorExpr:
		id, ok := fun.X.(*ast.Ident)
		if !ok || !isRuntimePkgIdent(id) {
			return false
		}
		p := seqImportPath(frame.ctx.file, id.Name)
		switch p {
		case "strings", "strconv", "reflect":
			return true
		}
		// 已证明的 database/sql 读取调用（查询快路径）。
		return isRuntimeQueryMethod(fun.Sel.Name)
	}
	return false
}

// seqPureBuiltinName 预声明纯内建（不改写包级变量）。
func seqPureBuiltinName(name string) bool {
	switch name {
	case "len", "cap", "append", "make", "new", "copy", "delete", "complex",
		"real", "imag", "min", "max", "print", "println", "panic",
		"int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16",
		"uint32", "uint64", "uintptr", "float32", "float64", "string",
		"bool", "byte", "rune":
		return true
	}
	return false
}

// markRangeGlobalWrite range 赋值目标为包级变量 → 绑定保守失效。
func (e *rtEval) markRangeGlobalWrite(target ast.Expr) {
	id, ok := target.(*ast.Ident)
	if !ok || id.Name == "" {
		return
	}
	if id.Obj != nil && !e.an.pkgVars[id.Obj] {
		return
	}
	if id.Obj == nil && !e.an.pkgVarNames[id.Name] {
		return
	}
	g := e.an.globalByNameOf(id)
	g.mutated = true
}

// markAddressTakenGlobal `&DB`（包级句柄取址）→ 绑定保守失效。
func (e *rtEval) markAddressTakenGlobal(expr ast.Expr) {
	id, ok := expr.(*ast.Ident)
	if !ok || id.Name == "" {
		return
	}
	if id.Obj != nil && !e.an.pkgVars[id.Obj] {
		return
	}
	if id.Obj == nil && !e.an.pkgVarNames[id.Name] {
		return
	}
	g := e.an.globalByNameOf(id)
	g.mutated = true
}

// markClosureWriters 闭包内的包级写者（未执行也保守失效）。
func (e *rtEval) markClosureWriters(body *ast.BlockStmt) {
	if body == nil {
		return
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range v.Lhs {
				e.markRangeGlobalWrite(lhs)
			}
		case *ast.IncDecStmt:
			e.markRangeGlobalWrite(v.X)
		case *ast.UnaryExpr:
			if v.Op == token.AND {
				e.markAddressTakenGlobal(v.X)
			}
		}
		return true
	})
}

// pkgFuncDecl 同包顶层函数声明（按包内文件扫描，用于跨文件引用/无索引回退）。
func (e *rtEval) pkgFuncDecl(frame *rtFrame, name string) (*ast.FuncDecl, *seqFileCtx, bool) {
	if frame == nil || frame.ctx == nil || name == "" {
		return nil, nil, false
	}
	rel := frame.ctx.rel
	for _, file := range seqPackageFilesOf(e.req, rel) {
		ctx := seqFileCtxFor(e.req, file)
		if ctx == nil || ctx.file == nil {
			continue
		}
		for _, decl := range ctx.file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || fd.Name == nil || fd.Name.Name != name {
				continue
			}
			id := "symbol:go:" + frame.pkgPath + ":" + name
			if e.extraFuncs == nil {
				e.extraFuncs = map[string]rtFuncSource{}
			}
			e.extraFuncs[id] = rtFuncSource{fn: fd, ctx: ctx}
			return fd, ctx, true
		}
	}
	return nil, nil, false
}

// evalArgEffects 只求实参副作用（纯值分支仍需先执行实参里的调用）。
func (e *rtEval) evalArgEffects(frame *rtFrame, args []ast.Expr, s *rtState, depth int) *rtState {
	if len(args) == 0 {
		return s
	}
	_, states := e.evalArgs(frame, args, s, depth)
	if len(states) > 0 {
		return states[0]
	}
	return s
}

// seqRuntimeShadowedBuiltin 内建名是否被同包（含跨文件）函数/变量遮蔽。
func seqRuntimeShadowedBuiltin(e *rtEval, frame *rtFrame, fun ast.Expr) bool {
	id, ok := fun.(*ast.Ident)
	if !ok || !seqPureBuiltinName(id.Name) {
		return false
	}
	if id.Obj != nil {
		// 解析到本文件内的声明（函数/变量/常量）→ 遮蔽了预声明内建。
		return true
	}
	// 跨文件声明（parser 不给 Obj）：按包内实际 FuncDecl 核对。
	if _, _, ok2 := e.pkgFuncDecl(frame, id.Name); ok2 {
		return true
	}
	return frame != nil && e.an != nil && e.an.pkgVarNames[id.Name]
}
