package action

// U04x（todo29）：整值复制的分配对象身份——复制前/后的别名共享同一分配根；
// 未知覆写/逃逸/可能未执行分支使根来源失效。

import (
	"go/ast"
	"go/token"
)

// seqSetAllocationOrigin 把分配对象的来源记到根变量及全部别名（本层标量槽）。
// origin=nil 表示来源未证明/被覆写。
func seqSetAllocationOrigin(req CodeSequenceRequest, root *ast.Object, dst *ast.Ident,
	origin *SeqStorageOrigin) {
	if req.env == nil {
		return
	}
	if root == nil {
		req.env.setScalar(dst.Obj, origin)
		return
	}
	touched := false
	for obj := range req.env.scalars {
		if seqPointerRootObj(req, obj) != root {
			continue
		}
		req.env.setScalar(obj, origin)
		touched = true
	}
	if !touched {
		req.env.setScalar(dst.Obj, origin)
	}
}

// seqPointerRoot 指针变量的分配根对象：`alias := clone` / `var alias = clone`
// 递归到声明来源；变量在函数体内被重新赋值（非声明处）则根未知（不猜）。
func seqPointerRoot(req CodeSequenceRequest, id *ast.Ident) *ast.Object {
	if id == nil || id.Obj == nil {
		return nil
	}
	return seqPointerRootObj(req, id.Obj)
}

func seqPointerRootObj(req CodeSequenceRequest, obj *ast.Object) *ast.Object {
	seen := map[*ast.Object]bool{}
	for obj != nil && !seen[obj] {
		seen[obj] = true
		if seqObjectRebound(req, obj) {
			return nil
		}
		var init *ast.Ident
		var declPos token.Pos
		switch d := obj.Decl.(type) {
		case *ast.AssignStmt:
			declPos = d.Pos()
			if len(d.Lhs) == len(d.Rhs) {
				for i, lhs := range d.Lhs {
					lid, ok := lhs.(*ast.Ident)
					if !ok || lid.Obj != obj || i >= len(d.Rhs) {
						continue
					}
					init, _ = runtimeUnparen(d.Rhs[i]).(*ast.Ident)
				}
			}
		case *ast.ValueSpec:
			declPos = d.Pos()
			if len(d.Names) == 1 && len(d.Values) == 1 {
				init, _ = runtimeUnparen(d.Values[0]).(*ast.Ident)
			}
		}
		_ = declPos
		if init == nil || init.Obj == nil {
			return obj
		}
		obj = init.Obj
	}
	return nil
}

// seqPointerRootIdent 变量的声明标识符（用于读声明类型）。
func seqPointerRootIdent(req CodeSequenceRequest, id *ast.Ident) *ast.Ident {
	if id == nil {
		return nil
	}
	root := seqPointerRoot(req, id)
	if root == nil {
		return id
	}
	// 回到该对象在声明处的标识符；没有时按对象名重建（类型解析只用 Obj）。
	if decl, ok := root.Decl.(*ast.AssignStmt); ok {
		for _, lhs := range decl.Lhs {
			if lid, ok2 := lhs.(*ast.Ident); ok2 && lid.Obj == root {
				return lid
			}
		}
	}
	if vs, ok := root.Decl.(*ast.ValueSpec); ok {
		for _, name := range vs.Names {
			if name.Obj == root {
				return name
			}
		}
	}
	if f, ok := root.Decl.(*ast.Field); ok {
		for _, name := range f.Names {
			if name.Obj == root {
				return name
			}
		}
	}
	return id
}

// seqObjectRebound 该对象在函数体内被重新赋值（非声明处）→ 别名根不再稳定。
func seqObjectRebound(req CodeSequenceRequest, obj *ast.Object) bool {
	if obj == nil {
		return false
	}
	declPos := seqDeclPosOf(obj.Decl)
	rebound := false
	if req.curBody == nil {
		return false
	}
	ast.Inspect(req.curBody, func(n ast.Node) bool {
		if rebound {
			return false
		}
		switch v := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range v.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && id.Obj == obj && v.Pos() != declPos {
					rebound = true
					return false
				}
			}
		case *ast.IncDecStmt:
			if id, ok := v.X.(*ast.Ident); ok && id.Obj == obj {
				rebound = true
				return false
			}
		}
		return true
	})
	return rebound
}
