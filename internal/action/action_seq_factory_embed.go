package action

// todo25 §3.4 剩余边界：嵌入提升方法/多条提升路径、闭包捕获后修改、
// 未知方法改写。全部按源码证明；证明不足时返回空（调用方保留原诊断）。

import (
	"go/ast"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// seqFindStructInPackage 在文件所在包里找类型声明的结构体。
func seqFindStructInPackage(req CodeSequenceRequest, rel, typeName string) (*seqFileCtx, *ast.StructType, bool) {
	for _, file := range seqPackageFilesOf(req, rel) {
		ctx := seqFileCtxFor(req, file)
		if ctx == nil || ctx.file == nil {
			continue
		}
		for _, decl := range ctx.file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, sp := range gd.Specs {
				ts, ok := sp.(*ast.TypeSpec)
				if !ok || ts.Name == nil || ts.Name.Name != typeName {
					continue
				}
				if st, ok := ts.Type.(*ast.StructType); ok {
					return ctx, st, true
				}
			}
		}
	}
	return nil, nil, false
}

// seqEmbeddedTypeNames 直接嵌入字段的类型名（同包；外部包/泛型实参保守跳过）。
func seqEmbeddedTypeNames(ctx *seqFileCtx, st *ast.StructType) []string {
	if ctx == nil || st == nil || st.Fields == nil {
		return nil
	}
	var out []string
	for _, fl := range st.Fields.List {
		if len(fl.Names) != 0 {
			continue // 具名字段不是嵌入
		}
		if name, ok := seqEmbeddedName(fl.Type); ok {
			out = append(out, name)
		}
	}
	return out
}

func seqEmbeddedName(t ast.Expr) (string, bool) {
	switch x := t.(type) {
	case *ast.Ident:
		return x.Name, true
	case *ast.StarExpr:
		return seqEmbeddedName(x.X)
	case *ast.IndexExpr:
		return seqEmbeddedName(x.X)
	case *ast.IndexListExpr:
		return seqEmbeddedName(x.X)
	case *ast.SelectorExpr:
		// 外部包嵌入：类型名可能同名；调用方只在同包节点存在时才会用到。
		return x.Sel.Name, true
	}
	return "", false
}

// seqFieldOwnerTypes 有该字段 writer 的类型：自身优先，否则沿嵌入链查找。
// 返回多个（多条提升路径）时调用方必须保守放弃。
func seqFieldOwnerTypes(a *Actions, req CodeSequenceRequest, rel, recvType, field string) []string {
	var out []string
	seen := map[string]bool{}
	var walk func(typeName string, depth int)
	walk = func(typeName string, depth int) {
		if typeName == "" || depth > 4 || seen[typeName] {
			return
		}
		seen[typeName] = true
		if len(seqFieldWriterParams(a, req, typeName, field)) > 0 {
			out = append(out, typeName)
			return
		}
		ctx, st, ok := seqFindStructInPackage(req, rel, typeName)
		if !ok {
			return
		}
		for _, emb := range seqEmbeddedTypeNames(ctx, st) {
			walk(emb, depth+1)
		}
	}
	walk(recvType, 0)
	return out
}

// seqMethodOwnerTypes 方法声明所在类型：自身没有该方法时沿嵌入链查找。
func seqMethodOwnerTypes(a *Actions, req CodeSequenceRequest, rel, recvType, method string) []string {
	pkg := pkgPathOf(req.curSym)
	var out []string
	seen := map[string]bool{}
	var walk func(typeName string, depth int)
	walk = func(typeName string, depth int) {
		if typeName == "" || depth > 4 || seen[typeName] {
			return
		}
		seen[typeName] = true
		id := "symbol:go:" + pkg + ":(" + typeName + ")." + method
		if _, err := a.repo.GetSymbol(domain.CanonicalID(id)); err == nil {
			out = append(out, typeName)
			return
		}
		ctx, st, ok := seqFindStructInPackage(req, rel, typeName)
		if !ok {
			return
		}
		for _, emb := range seqEmbeddedTypeNames(ctx, st) {
			walk(emb, depth+1)
		}
	}
	walk(recvType, 0)
	return out
}

// seqLocalFuncLits 工厂内局部函数值（闭包）声明对象 → 字面量。
func seqLocalFuncLits(fn *ast.FuncDecl) map[*ast.Object]*ast.FuncLit {
	out := map[*ast.Object]*ast.FuncLit{}
	if fn == nil || fn.Body == nil {
		return out
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			if len(x.Lhs) != 1 || len(x.Rhs) != 1 {
				return true
			}
			id, ok := x.Lhs[0].(*ast.Ident)
			lit, ok2 := x.Rhs[0].(*ast.FuncLit)
			if ok && ok2 && id.Obj != nil {
				out[id.Obj] = lit
			}
		case *ast.ValueSpec:
			for i, name := range x.Names {
				if i >= len(x.Values) {
					continue
				}
				if lit, ok := x.Values[i].(*ast.FuncLit); ok && name.Obj != nil {
					out[name.Obj] = lit
				}
			}
		}
		return true
	})
	return out
}

// seqFactoryCallsWithClosures 遍历工厂体（含一层/多层局部闭包体）的调用，
// 携带 switch/if 守卫。闭包捕获的返回实例按 AST 对象身份继续可见。
func seqFactoryCallsWithClosures(fn *ast.FuncDecl, resolve func(ast.Expr) []writeSource,
	visit func(*ast.CallExpr, string, map[*ast.Object][]writeSource)) {
	closures := seqLocalFuncLits(fn)
	var walk func(n ast.Node, guard string, depth int, subst map[*ast.Object][]writeSource)
	walk = func(n ast.Node, guard string, depth int, subst map[*ast.Object][]writeSource) {
		seqWalkFieldCalls(n, guard, func(call *ast.CallExpr, g string) {
			if depth < 4 {
				if id, ok := call.Fun.(*ast.Ident); ok && id.Obj != nil {
					if lit := closures[id.Obj]; lit != nil && lit.Body != nil {
						next := map[*ast.Object][]writeSource{}
						for k, v := range subst {
							next[k] = v
						}
						seqClosureArgTypes(lit.Type.Params, call.Args, resolve, next)
						walk(lit.Body, g, depth+1, next)
						return
					}
				}
			}
			visit(call, g, subst)
		})
	}
	walk(fn.Body, "", 0, nil)
}

// seqClosureArgTypes 闭包调用的实参类型按形参声明对象记录（闭包体里
// `box.Set(v)` 的 v 用它得到调用点具体类型，而不是接口形参类型）。
func seqClosureArgTypes(params *ast.FieldList, args []ast.Expr,
	resolve func(ast.Expr) []writeSource, out map[*ast.Object][]writeSource) {
	if params == nil {
		return
	}
	i := 0
	for _, fl := range params.List {
		n := len(fl.Names)
		if n == 0 {
			i++
			continue
		}
		var types []writeSource
		if i < len(args) {
			types = resolve(args[i])
		}
		for _, nm := range fl.Names {
			if nm.Obj != nil && len(types) > 0 {
				out[nm.Obj] = types
			}
		}
		i += n
	}
}

// seqFactoryUnknownMethod 调用是否可能改写返回实例（非已知字段 writer 的
// receiver 方法/把实例传给其它调用）。只对**返回实例**判定。
func seqFactoryUnknownMethod(call *ast.CallExpr, returned map[*ast.Object]bool, writers map[string]int) bool {
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		if id, ok2 := sel.X.(*ast.Ident); ok2 && returned[id.Obj] {
			if _, known := writers[sel.Sel.Name]; !known {
				return true
			}
			return false
		}
	}
	for _, arg := range call.Args {
		if id, ok := arg.(*ast.Ident); ok && returned[id.Obj] {
			return true
		}
	}
	return false
}

// seqFieldOwnerKey 字段 writer 查询失败时的后缀回退（原实现保留）。
func seqFieldOwnerKey(recvType, field string) string {
	return "." + recvType + "." + strings.TrimSpace(field)
}
