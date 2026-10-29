package action

import "go/ast"
import "go/token"
import "github.com/schaepher/codeintel/internal/domain"

// seqLocalInstanceDecl 定位局部实例声明 → (声明行, 具体类型名)。
func seqLocalInstanceDecl(f *ast.File, obj *ast.Object) (int, string, bool) {
	if obj == nil {
		return 0, "", false
	}
	switch d := obj.Decl.(type) {
	case *ast.AssignStmt:
		for i, lhs := range d.Lhs {
			id, ok := lhs.(*ast.Ident)
			if !ok || id.Obj != obj || i >= len(d.Rhs) {
				continue
			}
			if _, typ, ok := seqCompositeTypeName(d.Rhs[i]); ok {
				return 0, typ, true
			}
		}
	case *ast.ValueSpec:
		for i, nm := range d.Names {
			if nm.Obj != obj || i >= len(d.Values) {
				continue
			}
			if _, typ, ok := seqCompositeTypeName(d.Values[i]); ok {
				return 0, typ, true
			}
		}
	}
	return 0, "", false
}

// seqCompositeTypeName `&T{}` / `T{}` / `new(T)` → 类型名。
func seqCompositeTypeName(e ast.Expr) (string, string, bool) {
	switch x := e.(type) {
	case *ast.UnaryExpr:
		if x.Op == token.AND {
			return seqCompositeTypeName(x.X)
		}
	case *ast.ParenExpr:
		return seqCompositeTypeName(x.X)
	case *ast.CompositeLit:
		return seqTypeNameOfExpr(x.Type)
	case *ast.CallExpr:
		if id, ok := x.Fun.(*ast.Ident); ok && id.Name == "new" && id.Obj == nil && len(x.Args) == 1 {
			return seqTypeNameOfExpr(x.Args[0])
		}
	}
	return "", "", false
}

// seqTypeNameOfExpr 类型表达式 → 类型名（Ident / pkg.T / *T）。
func seqTypeNameOfExpr(t ast.Expr) (string, string, bool) {
	switch x := t.(type) {
	case *ast.Ident:
		return "", x.Name, true
	case *ast.StarExpr:
		return seqTypeNameOfExpr(x.X)
	case *ast.SelectorExpr:
		if id, ok := x.X.(*ast.Ident); ok {
			return id.Name + "." + x.Sel.Name, x.Sel.Name, true
		}
	}
	return "", "", false
}

// seqAssignTargetsObj 赋值左值是否是实例本身（`box = ...` / `*p` 不做处理）。
func seqAssignTargetsObj(lhs ast.Expr, obj *ast.Object) bool {
	id, ok := lhs.(*ast.Ident)
	return ok && id.Obj == obj
}

// seqCallPassesObj 调用是否把实例作为实参传入（可能逃逸被改写）。
func seqCallPassesObj(call *ast.CallExpr, obj *ast.Object) bool {
	for _, arg := range call.Args {
		if id, ok := arg.(*ast.Ident); ok && id.Obj == obj {
			return true
		}
	}
	return false
}

// seqFieldWriterParamsForType 类型上写该字段的方法 → 被写实参下标。
func (a *Actions) seqFieldWriterParamsForType(req CodeSequenceRequest, typeName, field string) map[string]int {
	return seqFieldWriterParams(a, req, typeName, field)
}

// seqMethodReadOnlyFor 方法是否可证明只读该字段（方法体 return <recv>.<field>，
// 且没有对字段的赋值）——用于把 getter 视为不改变字段值的调用。
func (a *Actions) seqMethodReadOnlyFor(req CodeSequenceRequest, typeName, method, field string) bool {
	fn, _, ok := seqFuncSource(a, req, "symbol:go:"+pkgOfID(domain.CanonicalID(req.curSym))+":("+typeName+")."+method)
	if !ok || fn == nil || fn.Body == nil {
		return false
	}
	read := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range x.Lhs {
				if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == field {
					read = false
					return false
				}
			}
		case *ast.ReturnStmt:
			for _, r := range x.Results {
				if sel, ok := r.(*ast.SelectorExpr); ok && sel.Sel.Name == field {
					read = true
				}
			}
		}
		return true
	})
	return read
}

// seqStructFieldNames 同文件内类型声明的字段名（结构体/嵌入字段）。
func seqStructFieldNames(f *ast.File, typeName string) []string {
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != typeName {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return false
		}
		for _, fl := range st.Fields.List {
			for _, nm := range fl.Names {
				out = append(out, nm.Name)
			}
		}
		return false
	})
	return out
}
