package action

import (
	"go/ast"
	"go/token"
	"strings"
)

// findFuncBody 按索引行号定位函数体（行号落在声明区间内即命中）。
func findFuncBody(fset *token.FileSet, f *ast.File, line int) *ast.BlockStmt {
	body, _ := findFuncDecl(fset, f, line)
	return body
}

// findFuncDecl 按索引行号定位函数声明（body + 声明——返回槽/参数类型需要）。
func findFuncDecl(fset *token.FileSet, f *ast.File, line int) (*ast.BlockStmt, *ast.FuncDecl) {
	var body *ast.BlockStmt
	var decl *ast.FuncDecl
	ast.Inspect(f, func(node ast.Node) bool {
		fd, ok := node.(*ast.FuncDecl)
		if !ok || fd.Body == nil || body != nil {
			return true
		}
		start := fset.Position(fd.Pos()).Line
		end := fset.Position(fd.End()).Line
		if line >= start && line <= end {
			body, decl = fd.Body, fd
			return false
		}
		return true
	})
	return body, decl
}

// effectVarTypes 形参/接收者/局部变量的类型短名（字段身份匹配用）。
func effectVarTypes(fset *token.FileSet, fn *ast.FuncDecl, src []byte) map[string]string {
	out := map[string]string{}
	addFields := func(fl *ast.FieldList) {
		if fl == nil {
			return
		}
		for _, f := range fl.List {
			typ := starTypeName(f.Type)
			for _, n := range f.Names {
				out[n.Name] = typ
			}
		}
	}
	addFields(fn.Recv)
	addFields(fn.Type.Params)
	if fn.Body == nil {
		return out
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch x := n.(type) {
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
					t := typ
					if t == "" && len(vs.Values) == len(vs.Names) {
						t = typeFromValue(vs.Values[i])
					}
					if t != "" {
						out[name.Name] = t
					}
				}
			}
		case *ast.AssignStmt:
			if len(x.Lhs) != len(x.Rhs) {
				return true
			}
			for i, lhs := range x.Lhs {
				id, ok := lhs.(*ast.Ident)
				if !ok || id.Name == "_" {
					continue
				}
				if t := typeFromValue(x.Rhs[i]); t != "" {
					out[id.Name] = t
				}
			}
		}
		return true
	})
	return out
}

// starTypeName 类型表达式 → 短类型名（剥指针/括号）。
func starTypeName(t ast.Expr) string {
	for {
		switch x := t.(type) {
		case *ast.StarExpr:
			t = x.X
			continue
		case *ast.ParenExpr:
			t = x.X
			continue
		}
		return exprTypeName(t)
	}
}

// typeFromValue 赋值右值的类型短名（复合字面量/构造器/类型转换）。
func typeFromValue(e ast.Expr) string {
	if t := implTypeFromExpr(e); t != "" {
		return t
	}
	switch x := e.(type) {
	case *ast.UnaryExpr:
		return typeFromValue(x.X)
	case *ast.ParenExpr:
		return typeFromValue(x.X)
	}
	return ""
}

// errLikeName 变量名是否像错误（err / *Err / e）。
func errLikeName(name string) bool {
	l := strings.ToLower(name)
	return l == "err" || l == "e" || strings.HasSuffix(l, "err") || strings.HasSuffix(l, "error")
}

// isNilIdent 表达式是否为 nil 字面量。
func isNilIdent(exprs []ast.Expr) bool {
	if len(exprs) != 1 {
		return false
	}
	id, ok := exprs[0].(*ast.Ident)
	return ok && id.Name == "nil"
}

// callName 调用表达式的被调名（Ident / Selector 末段）。
func callName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		return fun.Sel.Name
	}
	return ""
}

// fileLine "file:line" 文本。
func fileLine(file string, line int) string {
	return file + ":" + itoaSeq(line)
}

// nodeText 源码片段（按位置取原文——保留字面形态）。
func nodeText(fset *token.FileSet, src []byte, n ast.Node) string {
	start := fset.Position(n.Pos()).Offset
	end := fset.Position(n.End()).Offset
	if start < 0 || end > len(src) || start >= end {
		return ""
	}
	return string(src[start:end])
}

// dedupStrings 去重（保持顺序）。
func dedupStrings(xs []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		if x == "" || seen[x] {
			continue
		}
		seen[x] = true
		out = append(out, x)
	}
	return out
}

// sortStrings 就地字典序（小工具，避免为此引入 sort 依赖顺序混乱）。
func sortStrings(xs []string) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j] < xs[j-1]; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}
