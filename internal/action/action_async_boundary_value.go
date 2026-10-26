package action

// todo16-6：接收侧的值/实例/作用域分析（不靠同名变量或字段叶名）。

import (
	"go/ast"
	"go/token"
)

// varTypeMap 局部变量/包级变量 → 类型名（接收者/实例身份；只支持可直接
// 读出的声明形态，未知返回空——不猜）。
func varTypeMap(pf *parsedFunc) map[string]string {
	out := map[string]string{}
	add := func(name, typ string) {
		if name != "" && typ != "" {
			out[name] = typ
		}
	}
	scan := func(root ast.Node) {
		if root == nil {
			return
		}
		ast.Inspect(root, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.AssignStmt:
				for i, lhs := range x.Lhs {
					id, ok := lhs.(*ast.Ident)
					if !ok || len(x.Rhs) != len(x.Lhs) {
						continue
					}
					if t := instanceTypeName(x.Rhs[i]); t != "" {
						add(id.Name, t)
					}
				}
			case *ast.GenDecl:
				for _, sp := range x.Specs {
					vs, ok := sp.(*ast.ValueSpec)
					if !ok {
						continue
					}
					typ := exprTypeName(vs.Type)
					for i, name := range vs.Names {
						if typ == "" && len(vs.Values) == len(vs.Names) {
							typ = instanceTypeName(vs.Values[i])
						}
						add(name.Name, typ)
					}
				}
			case *ast.DeclStmt:
				gd, ok := x.Decl.(*ast.GenDecl)
				if !ok {
					return true
				}
				for _, sp := range gd.Specs {
					vs, ok := sp.(*ast.ValueSpec)
					if !ok {
						continue
					}
					typ := exprTypeName(vs.Type)
					for i, name := range vs.Names {
						if typ == "" && len(vs.Values) == len(vs.Names) {
							typ = instanceTypeName(vs.Values[i])
						}
						add(name.Name, typ)
					}
				}
			}
			return true
		})
	}
	scan(pf.parsed)
	scan(pf.decl.Body)
	return out
}

// instanceTypeName &T{} / T{} / *T 的类型短名。
func instanceTypeName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.UnaryExpr:
		return instanceTypeName(x.X)
	case *ast.ParenExpr:
		return instanceTypeName(x.X)
	case *ast.CompositeLit:
		return exprTypeName(x.Type)
	}
	return ""
}

// blockContaining 包含 pos 的最内层块（作用域判断用）。
func blockContaining(root *ast.BlockStmt, fset *token.FileSet, pos token.Pos) *ast.BlockStmt {
	best := root
	bestSize := fset.Position(root.End()).Offset - fset.Position(root.Pos()).Offset
	ast.Inspect(root, func(n ast.Node) bool {
		b, ok := n.(*ast.BlockStmt)
		if !ok || !(pos >= b.Pos() && pos <= b.End()) {
			return true
		}
		size := fset.Position(b.End()).Offset - fset.Position(b.Pos()).Offset
		if size <= bestSize {
			best, bestSize = b, size
		}
		return true
	})
	return best
}

// assignBinding 一个名字的赋值定义（接收绑定/重赋值/遮蔽）。
type assignBinding struct {
	stmt   *ast.AssignStmt
	line   int
	rhs    ast.Expr
	block  *ast.BlockStmt
	define bool
}

// reachingBinding name 在 pos 处的到达赋值（最内层块 + 最近位置）。
func reachingBinding(pf *parsedFunc, name string, pos token.Pos) *assignBinding {
	var best *assignBinding
	bs := declaredBindings(pf, name)
	for i := range bs {
		b := &bs[i]
		if b.line == 0 || b.stmt == nil {
			continue
		}
		if b.stmt.Pos() > pos || !blockContains(b.block, pf.fset, pos) {
			continue
		}
		if best == nil || b.stmt.Pos() > best.stmt.Pos() ||
			(b.stmt.Pos() == best.stmt.Pos() && blockSize(pf, b.block) < blockSize(pf, best.block)) {
			best = b
		}
	}
	return best
}

// declaredBindings 收集 name 的所有赋值/声明（不含复合字面量字段）。
func declaredBindings(pf *parsedFunc, name string) []assignBinding {
	var out []assignBinding
	ast.Inspect(pf.decl.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range x.Lhs {
				id, ok := lhs.(*ast.Ident)
				if !ok || id.Name != name {
					continue
				}
				b := assignBinding{stmt: x, line: pf.fset.Position(x.Pos()).Line,
					block: blockContaining(pf.decl.Body, pf.fset, x.Pos()), define: x.Tok == token.DEFINE}
				if len(x.Rhs) == len(x.Lhs) {
					b.rhs = x.Rhs[i]
				} else if len(x.Rhs) > 0 {
					b.rhs = x.Rhs[0]
				}
				out = append(out, b)
			}
		case *ast.DeclStmt:
			gd, ok := x.Decl.(*ast.GenDecl)
			if !ok {
				return true
			}
			for _, sp := range gd.Specs {
				vs, ok := sp.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, id := range vs.Names {
					if id.Name != name {
						continue
					}
					b := assignBinding{line: pf.fset.Position(x.Pos()).Line,
						block: blockContaining(pf.decl.Body, pf.fset, x.Pos())}
					if len(vs.Values) == len(vs.Names) {
						b.rhs = vs.Values[i]
					}
					out = append(out, b)
				}
			}
		}
		return true
	})
	return out
}

func blockContains(b *ast.BlockStmt, fset *token.FileSet, pos token.Pos) bool {
	return b != nil && pos >= b.Pos() && pos <= b.End()
}

func blockSize(pf *parsedFunc, b *ast.BlockStmt) int {
	if b == nil {
		return 1 << 30
	}
	return pf.fset.Position(b.End()).Offset - pf.fset.Position(b.Pos()).Offset
}

// isReceiveCall 调用是否为受支持的 Receive 类 API（用于值来源判定）。
func isReceiveCall(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && asyncConsumerCalls[toLowerASCII(sel.Sel.Name)]
}

func toLowerASCII(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

// resolveInstanceRoot 表达式最终指向的实例变量（别名/&/直接变量）。
// fromReceive 表示该实例来自 Receive 载荷绑定；newValue 表示由构造/其他
// 调用产生的新值（不能等同载荷）。
func resolveInstanceRoot(pf *parsedFunc, e ast.Expr, before token.Pos, depth int) (root string, fromReceive, newValue bool) {
	if e == nil || depth > 6 {
		return "", false, false
	}
	switch x := e.(type) {
	case *ast.ParenExpr:
		return resolveInstanceRoot(pf, x.X, before, depth+1)
	case *ast.UnaryExpr:
		if x.Op == token.AND {
			return resolveInstanceRoot(pf, x.X, before, depth+1)
		}
	case *ast.Ident:
		b := reachingBinding(pf, x.Name, before)
		if b == nil || b.rhs == nil {
			return x.Name, false, false
		}
		// 直接来自 Receive 的绑定（tuple 第二值等）：由调用方按 payloadVar 判定
		if isReceiveCall(b.rhs) {
			return x.Name, true, false
		}
		if _, isIdent := b.rhs.(*ast.Ident); isIdent {
			r, fr, nv := resolveInstanceRoot(pf, b.rhs, b.stmt.Pos(), depth+1)
			if r != "" {
				return r, fr, nv
			}
		}
		if isFreshValue(b.rhs) {
			return x.Name, false, true
		}
		return x.Name, false, false
	case *ast.CallExpr:
		return "", false, true
	}
	return "", false, false
}

// isFreshValue 构造新实例/新值的右值（复合字面量、其他调用）。
func isFreshValue(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.CompositeLit:
		return true
	case *ast.UnaryExpr:
		return isFreshValue(x.X)
	case *ast.CallExpr:
		return !isReceiveCall(x)
	}
	return false
}

// fieldReadBase 在 readLine 找到 leaf 字段读取的基表达式（读取实例）。
func fieldReadBase(pf *parsedFunc, leaf string, readLine int) (ast.Expr, ast.Expr, bool) {
	var base, full ast.Expr
	ast.Inspect(pf.decl.Body, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != leaf {
			return true
		}
		if pf.fset.Position(sel.Pos()).Line != readLine {
			return true
		}
		base, full = sel.X, sel
		return false
	})
	return base, full, base != nil
}
