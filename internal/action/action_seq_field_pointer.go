package action

// B03a：指针中间字段的分配证明（同一对象的 CompositeLit/赋值初始化）。

import (
	"go/ast"
	"go/token"
)

// seqPointerFieldAllocated 证明 root 的字段路径 prefix 指向一个已分配的具体对象：
//   - `root := T{Field: new(U)}` / `Field: &U{}`
//   - 调用点之前 `root.Field = new(U)` / `&U{}`
//
// 只接受 new(T)/&T{} 且类型名与字段声明类型一致；重绑/另一分配在失效规则里
// 清掉嵌套字段槽。
func seqPointerFieldAllocated(req CodeSequenceRequest, root *ast.Ident, prefix []string,
	fd seqResolvedField, callPos token.Pos) bool {
	if root == nil || root.Obj == nil || len(prefix) == 0 || req.curCtx == nil {
		return false
	}
	want := fd.ref.name
	check := func(e ast.Expr) bool {
		u, ok := runtimeUnparen(e).(*ast.UnaryExpr)
		if !ok || u.Op != token.AND {
			return false
		}
		lit, ok := runtimeUnparen(u.X).(*ast.CompositeLit)
		if !ok {
			return false
		}
		ref, ok := seqTypeExprRefAtCursor(req, lit.Type)
		return ok && ref.name == want
	}
	checkNew := func(e ast.Expr) bool {
		call, ok := runtimeUnparen(e).(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return false
		}
		id, ok := call.Fun.(*ast.Ident)
		if !ok || id.Name != "new" || id.Obj != nil {
			return false
		}
		ref, ok := seqTypeExprRefAtCursor(req, call.Args[0])
		return ok && ref.name == want
	}
	okInit := func(e ast.Expr) bool { return checkNew(e) || check(e) }
	// 声明处 CompositeLit：root := T{Field: new(U)} / var root = T{...}
	var init ast.Expr
	switch d := root.Obj.Decl.(type) {
	case *ast.AssignStmt:
		if len(d.Lhs) == len(d.Rhs) {
			for i, lhs := range d.Lhs {
				if lid, ok := lhs.(*ast.Ident); ok && lid.Obj == root.Obj {
					init = d.Rhs[i]
				}
			}
		}
	case *ast.ValueSpec:
		for i, n := range d.Names {
			if n.Obj == root.Obj && i < len(d.Values) {
				init = d.Values[i]
			}
		}
	}
	if lit, ok := runtimeUnparen(init).(*ast.CompositeLit); ok && len(prefix) == 1 {
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if id, ok := kv.Key.(*ast.Ident); ok && id.Name == prefix[0] {
				return okInit(kv.Value)
			}
		}
	}
	// 调用点之前的赋值：root.Field = new(U)
	if req.curBody == nil {
		return false
	}
	ast.Inspect(req.curBody, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || as.Pos() >= callPos || len(as.Lhs) != len(as.Rhs) {
			return true
		}
		for i, lhs := range as.Lhs {
			sel, ok := runtimeUnparen(lhs).(*ast.SelectorExpr)
			if !ok {
				continue
			}
			id, ok := runtimeUnparen(sel.X).(*ast.Ident)
			if !ok || id.Obj != root.Obj || sel.Sel.Name != prefix[0] {
				continue
			}
			if len(prefix) == 1 && okInit(as.Rhs[i]) {
				return false
			}
		}
		return true
	})
	// 上面的 Inspect 返回值无法直接表示命中；用闭包变量记录。
	return seqPointerAssignProof(req, root, prefix, callPos, okInit)
}

// seqPointerAssignProof 扫描调用点之前的赋值（返回是否命中）。
func seqPointerAssignProof(req CodeSequenceRequest, root *ast.Ident, prefix []string,
	callPos token.Pos, okInit func(ast.Expr) bool) bool {
	found := false
	ast.Inspect(req.curBody, func(n ast.Node) bool {
		if found {
			return false
		}
		as, ok := n.(*ast.AssignStmt)
		if !ok || as.Pos() >= callPos || len(as.Lhs) != len(as.Rhs) {
			return true
		}
		for i, lhs := range as.Lhs {
			sel, ok := runtimeUnparen(lhs).(*ast.SelectorExpr)
			if !ok {
				continue
			}
			id, ok := runtimeUnparen(sel.X).(*ast.Ident)
			if ok && id.Obj == root.Obj && sel.Sel.Name == prefix[0] && len(prefix) == 1 &&
				okInit(as.Rhs[i]) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}
