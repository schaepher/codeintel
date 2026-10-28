package action

// todo22 §2.1：函数值捕获的**统一入口**——所有调用点（普通调用、外部/未证明
// 调用、未解析分支、测试调用者）都只通过这些函数取值，不允许各自直连底层
// 求值函数（否则"到达定义/绑定口径"会分叉）。
//
// 三个入口与职责：
//   - captureSeqClosureValue：单个函数值（字面量/变量/形参/包级函数/方法值/
//     工厂返回），失败时返回**明确的 unknown**（带原因），不返回零值占位。
//   - captureSeqClosureArgs：一次调用的实参值（按位置；`f(list...)` 摊平）。
//   - captureSeqCallbackLists：函数列表（[]func 字面量/append/变参/range 对象）。
//
// 绑定元数据随值一起保存（todo22 §2.1）：词法文件与 AST（seqFileCtx）、包
// （pkgPath）、receiver（recvType/recvDecl）、声明对象（declObj）、调用上下文
// （bindFile/bindLine）、条件（cond），并用 `*ast.Object` 身份区分同名阴影。

import (
	"go/ast"
	"go/token"

	"github.com/schaepher/codeintel/internal/domain"
)

// captureSeqClosureValue 捕获单个表达式的函数值（唯一入口）。
func captureSeqClosureValue(a *Actions, req CodeSequenceRequest, ctx *seqFileCtx, expr ast.Expr) seqValue {
	return captureSeqClosureValueDepth(a, req, ctx, expr, 0)
}

// captureSeqClosureValueDepth 带工厂递归深度上限的捕获（内部递归用；避免
// 自引用工厂无限展开）。
func captureSeqClosureValueDepth(a *Actions, req CodeSequenceRequest, ctx *seqFileCtx, expr ast.Expr, depth int) seqValue {
	v := seqValueOfExprDepth(a, req, ctx, req.env, expr, depth)
	// 方法值：x.Method——接收者类型来自本函数内按**对象身份**的类型数据流
	// （构造器/字面量赋值），不按变量名全局搜同名赋值。
	if v.kind == seqValueUnknown {
		if id, ok := expr.(*ast.SelectorExpr); ok {
			if mv, ok2 := seqMethodValue(a, req, ctx, id); ok2 {
				return mv
			}
		}
	}
	return v
}

// captureSeqClosureArgs 捕获一次调用的实参值（形参绑定用）。
func captureSeqClosureArgs(a *Actions, req CodeSequenceRequest, ctx *seqFileCtx, call *ast.CallExpr) []seqValue {
	if call == nil {
		return nil
	}
	out := make([]seqValue, 0, len(call.Args))
	for i, arg := range call.Args {
		v := captureSeqClosureValue(a, req, ctx, arg)
		v.origin = seqOriginArg
		v.bindFile, v.bindLine = req.curFile, lineOfOffset(ctx, call.Lparen)
		// todo22 §2.4：`f(list...)` 把列表元素摊平为实参
		if call.Ellipsis.IsValid() && i == len(call.Args)-1 && v.kind == seqValueList {
			out = append(out, v.elems...)
			continue
		}
		out = append(out, v)
	}
	return out
}

// captureSeqCallbackLists 捕获"函数列表"表达式（字面量/append/变量绑定）。
func captureSeqCallbackLists(a *Actions, req CodeSequenceRequest, ctx *seqFileCtx, expr ast.Expr) (seqValue, bool) {
	if expr == nil {
		return seqValue{}, false
	}
	if id, ok := expr.(*ast.Ident); ok {
		if req.env != nil {
			if v, ok2 := req.env.lookup(id.Obj); ok2 && v.kind == seqValueList {
				return v, true
			}
		}
		return seqValue{}, false
	}
	v := captureSeqClosureValue(a, req, ctx, expr)
	if v.kind != seqValueList {
		return seqValue{}, false
	}
	return v, true
}

// seqMethodValue `x.Method` 的方法值：接收者类型 = 变量在本函数内的类型数据流。
// 只接受**同一声明对象**的类型（阴影/无关实例不借用）；符号必须真实存在于索引。
func seqMethodValue(a *Actions, req CodeSequenceRequest, ctx *seqFileCtx, sel *ast.SelectorExpr) (seqValue, bool) {
	id, ok := sel.X.(*ast.Ident)
	if !ok || (id.Obj != nil && id.Obj.Kind == ast.Pkg) {
		return seqValue{}, false
	}
	ref, ok := seqLookupType(req.env, id.Obj)
	if !ok {
		return seqValue{}, false
	}
	symID := "symbol:go:" + ref.pkgPath + ":(" + ref.typeName + ")." + sel.Sel.Name
	if _, err := a.repo.GetSymbol(domain.CanonicalID(symID)); err != nil {
		return unknownSeqValue("方法值 " + ref.typeName + "." + sel.Sel.Name +
			" 在索引中不存在（receiver 类型来自类型数据流，但方法未索引）"), true
	}
	v := namedSeqValue(symID)
	v.bindFile, v.bindLine = req.curFile, lineOfOffset(ctx, sel.Sel.Pos())
	v.declObj = id.Obj
	return v, true
}

// seqTypeOfExpr 表达式的接收者类型（构造器/字面量/变量链；纯语法数据流）。
func seqTypeOfExpr(a *Actions, req CodeSequenceRequest, ctx *seqFileCtx, expr ast.Expr) (seqTypeRef, bool) {
	switch e := expr.(type) {
	case *ast.UnaryExpr:
		if e.Op.String() == "&" {
			return seqTypeOfExpr(a, req, ctx, e.X)
		}
	case *ast.CompositeLit:
		return seqTypeRefOf(req, ctx, e.Type)
	case *ast.ParenExpr:
		return seqTypeOfExpr(a, req, ctx, e.X)
	case *ast.CallExpr:
		// T06：类型表达式调用 `(*T)(nil)` / `(T)(nil)` ——参数是 nil 时，
		// 被调者就是类型本身（reflect.TypeOf 的常见写法）。
		if isNilIdent(e.Args) {
			if ref, ok := seqTypeRefOf(req, ctx, unwrapParen(e.Fun)); ok {
				return ref, true
			}
		}
		// T06：reflect.TypeOf((*T)(nil)) / TypeOf(T{}) / TypeOf(new(T)) ——
		// 反射注册的 impl 类型（容器契约形态）直接从参数里取类型。
		if sel, ok := e.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "TypeOf" && len(e.Args) == 1 {
			if id, ok2 := sel.X.(*ast.Ident); ok2 && id.Obj == nil {
				if p := seqImportPath(ctx.file, id.Name); p == "reflect" {
					return seqTypeOfExpr(a, req, ctx, e.Args[0])
				}
			}
		}
		// 构造器：newT() / NewT() ——按返回值类型（rhsTypes 已有该能力）
		imports := importAliases(ctx.file)
		refs := rhsTypes(a, req, ctx.file, imports, e, 3, pkgPathOf(req.curSym))
		if len(refs) == 1 {
			return seqTypeRef{pkgPath: refs[0].pkgPath, typeName: refs[0].typeName}, true
		}
		// todo22 P1：外部包构造器（`ext.NewStore()`）——本仓没有索引边，
		// 按需读外部源码的第一个返回类型（离线只读，请求内缓存）。
		if ref, ok := seqExternalFuncResultType(a, req, ctx, e); ok {
			return ref, true
		}
	case *ast.Ident:
		return seqLookupType(req.env, e.Obj)
	}
	return seqTypeRef{}, false
}

// unwrapParen 剥掉括号（`(*T)` → `*T`）。
func unwrapParen(e ast.Expr) ast.Expr {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			return e
		}
		e = p.X
	}
}

// seqTypeRefOf 类型表达式 → 类型引用（Ident / pkg.T / 泛型 T[P]）。
func seqTypeRefOf(req CodeSequenceRequest, ctx *seqFileCtx, t ast.Expr) (seqTypeRef, bool) {
	switch x := t.(type) {
	case *ast.Ident:
		return seqTypeRef{pkgPath: pkgPathOf(req.curSym), typeName: x.Name}, true
	case *ast.SelectorExpr:
		if id, ok := x.X.(*ast.Ident); ok {
			if p := seqImportPath(ctx.file, id.Name); p != "" {
				return seqTypeRef{pkgPath: p, typeName: x.Sel.Name}, true
			}
		}
	case *ast.StarExpr:
		return seqTypeRefOf(req, ctx, x.X)
	case *ast.Ellipsis:
		return seqTypeRefOf(req, ctx, x.Elt)
	case *ast.IndexExpr:
		return seqTypeRefOf(req, ctx, x.X)
	case *ast.IndexListExpr:
		return seqTypeRefOf(req, ctx, x.X)
	}
	return seqTypeRef{}, false
}

// seqLookupType 按声明对象查类型（沿闭包父环境）。
func seqLookupType(env *seqEnv, obj *ast.Object) (seqTypeRef, bool) {
	for e := env; e != nil; e = e.parent {
		if r, ok := e.types[obj]; ok {
			return r, true
		}
	}
	return seqTypeRef{}, false
}

// lineOfOffset 位置 → 行号（ctx 可能为 nil）。
func lineOfOffset(ctx *seqFileCtx, pos token.Pos) int {
	if ctx == nil || ctx.fset == nil || !pos.IsValid() {
		return 0
	}
	return ctx.fset.Position(pos).Line
}

// seqReceiverTypeRef 方法调用接收者的类型（`extT.Method()` / `h.store.Method()`）：
//  1. 局部变量：按声明对象查类型数据流（声明/复合字面量/构造器返回）；
//  2. 当前 receiver 的字段：按结构体声明取字段类型（`s.store.Method()`）；
//  3. 已知类型的变量字段：同文件结构体声明（不做跨文件猜测）。
func seqReceiverTypeRef(a *Actions, req CodeSequenceRequest, ctx *seqFileCtx,
	sel *ast.SelectorExpr) (seqTypeRef, bool) {
	if sel == nil {
		return seqTypeRef{}, false
	}
	switch x := sel.X.(type) {
	case *ast.Ident:
		if r, ok := seqLookupType(req.env, x.Obj); ok {
			return r, true
		}
	case *ast.SelectorExpr:
		base, ok := x.X.(*ast.Ident)
		if !ok {
			return seqTypeRef{}, false
		}
		if ref, ok := seqLookupType(req.env, base.Obj); ok {
			if fr, ok2 := seqStructFieldTypeRef(req, ctx, ref.typeName, x.Sel.Name); ok2 {
				// 裸类型名（`store *Store`）按 base 的包解析；限定名
				// （`store *ext.Store`）已由 seqTypeRefOf 给出准确 import path，
				// 不能被覆盖。
				if fr.pkgPath == pkgPathOf(req.curSym) {
					fr.pkgPath = ref.pkgPath
				}
				return fr, true
			}
		}
		// 当前 receiver 的字段（receiver 变量本身不在 env 里）
		if req.curRecvType != "" {
			if fr, ok2 := seqStructFieldTypeRef(req, ctx, req.curRecvType, x.Sel.Name); ok2 {
				return fr, true
			}
		}
	}
	return seqTypeRef{}, false
}

// seqStructFieldTypeRef 同文件结构体声明的字段类型（含 pkg.T / *T / 泛型）。
func seqStructFieldTypeRef(req CodeSequenceRequest, ctx *seqFileCtx, typeName, field string) (seqTypeRef, bool) {
	if ctx == nil || ctx.file == nil || typeName == "" || field == "" {
		return seqTypeRef{}, false
	}
	for _, decl := range ctx.file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, sp := range gd.Specs {
			tsp, ok := sp.(*ast.TypeSpec)
			if !ok || tsp.Name == nil || tsp.Name.Name != typeName {
				continue
			}
			st, ok := tsp.Type.(*ast.StructType)
			if !ok || st.Fields == nil {
				continue
			}
			for _, fl := range st.Fields.List {
				for _, nm := range fl.Names {
					if nm.Name == field {
						return seqTypeRefOf(req, ctx, fl.Type)
					}
				}
			}
		}
	}
	return seqTypeRef{}, false
}

// seqExternalFuncResultType 外部包包级函数/构造器的第一个返回类型。
func seqExternalFuncResultType(a *Actions, req CodeSequenceRequest, ctx *seqFileCtx, call *ast.CallExpr) (seqTypeRef, bool) {
	if ctx == nil || ctx.file == nil || call == nil {
		return seqTypeRef{}, false
	}
	importPath, declName, _, ok := seqExternalCallTarget(ctx.file, call.Fun)
	if !ok || req.pkgSet[importPath] {
		return seqTypeRef{}, false
	}
	pkg := seqExternalPackage(a, req, importPath)
	if pkg == nil || pkg.err != nil || pkg.std {
		return seqTypeRef{}, false
	}
	ectx, fd := seqFindExternalDecl(pkg, declName)
	if ectx == nil || fd == nil || fd.Type.Results == nil || len(fd.Type.Results.List) == 0 {
		return seqTypeRef{}, false
	}
	sub := req
	sub.curSym = "symbol:go:" + importPath + ":" + declName
	return seqTypeRefOf(sub, ectx, fd.Type.Results.List[0].Type)
}
