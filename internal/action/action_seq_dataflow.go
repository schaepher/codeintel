package action

import "go/ast"
import "go/parser"
import "go/token"
import "os"
import "path/filepath"
import "strings"
import "github.com/schaepher/codeintel/internal/domain"

// recvTypeName receiver 类型 AST → 类型名（*T / T → T；跨包形态
// pkg.T → T——同包字段写入常见）。
func recvTypeName(t ast.Expr) string {
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	if sel, ok := t.(*ast.SelectorExpr); ok {
		return sel.Sel.Name
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// receiverFieldSel 调用 fun（s.manager.SubmitOrder）是否为 receiver
// 字段方法调用——s 是当前方法 receiver 名（recvType 对应）、X 是
// SelectorExpr（s.manager）→ 返回字段名 manager。
func receiverFieldSel(sel *ast.SelectorExpr, recvType string) (string, bool) {
	x, ok := sel.X.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}

	if _, isID := x.X.(*ast.Ident); !isID {
		return "", false
	}
	return x.Sel.Name, true
}

// writeSource 字段赋值来源候选（类型名 + 包路径）——P0-3 扩展。
type writeSource struct {
	typeName string
	pkgPath  string
}

// receiverFieldImpl 数据流具体化（R97-2 + P0-3）：receiver 字段
// （s.manager）的赋值来源类型——查摘要 direct_write 写入函数 → 读源码
// AST 找赋值表达式（x.manager = ...）的类型来源（字面量/构造器/变量
// 链/条件分支多候选）→ 构造 (Impl).Method。找不到（无写入摘要/形态
// 不支持）返回空——调用方 fallback 接口类型匹配（InterfaceMethodImpl）。
func (a *Actions) receiverFieldImpl(req CodeSequenceRequest, recvType, field, method string) string {

	writers, err := a.repo.GetFieldWriters("." + recvType + "." + field)
	if err != nil || len(writers) == 0 {
		return ""
	}

	for _, w := range writers {
		for _, c := range a.fieldWriteTypes(req, w, field) {
			return GrpcMethodEntryID("symbol:go:"+c.pkgPath+":"+c.typeName, method)
		}
	}
	return ""
}

// fieldWriteTypes 读写入函数源码，找 "x.<field> = <X>" 赋值中 X 的类型
// 来源（P0-3 扩展——R97-2 仅 &T{} 字面量）：
//   - 字面量：&T{} / T{}（Ident 同包；SelectorExpr pkg.T 跨包——import
//     映射解析包路径）
//   - 构造器调用：newX() / pkg.NewX() → 函数返回类型（优先函数体
//     return RHS——"声明返回接口、函数体 return 具体实现"形态；跨包
//     经索引节点读源码）
//   - 变量链：x.<field> = v → 向上回溯 v 的初始化赋值（深度 ≤3 防环）
//
// 条件分支各分支的赋值都收集（多候选——去重后返回）。
func (a *Actions) fieldWriteTypes(req CodeSequenceRequest, writerID, field string) []writeSource {
	n, err := a.repo.GetSymbol(domain.CanonicalID(writerID))
	if err != nil || n.FilePath == "" {
		return nil
	}
	src, err := os.ReadFile(filepath.Join(req.RepoAbs, n.FilePath))
	if err != nil {
		return nil
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, n.FilePath, src, 0)
	if err != nil {
		return nil
	}

	curPkg := pkgPathOf(writerID)
	imports := importAliases(f)
	var out []writeSource
	ast.Inspect(f, func(node ast.Node) bool {
		as, ok := node.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		sel, ok := as.Lhs[0].(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != field {
			return true
		}
		out = appendUnique(out, rhsTypes(a, req, f, imports, as.Rhs[0], 3, curPkg)...)
		return true
	})
	return out
}

// importAliases 文件 import 块 → alias → 包路径（默认 alias = 包末段；
// _ / . 导入跳过）。
func importAliases(f *ast.File) map[string]string {
	m := map[string]string{}
	for _, imp := range f.Imports {
		pkgPath := strings.Trim(imp.Path.Value, `"`)
		alias := pkgPath
		if i := strings.LastIndex(alias, "/"); i >= 0 {
			alias = alias[i+1:]
		}
		if imp.Name != nil {
			if imp.Name.Name == "_" || imp.Name.Name == "." {
				continue
			}
			alias = imp.Name.Name
		}
		m[alias] = pkgPath
	}
	return m
}

// rhsTypes RHS 表达式 → 类型来源候选（字面量/构造器/变量链递归；
// depth 为变量链回溯剩余深度——防环）。
func rhsTypes(a *Actions, req CodeSequenceRequest, f *ast.File, imports map[string]string, expr ast.Expr, depth int, curPkg string) []writeSource {
	switch e := expr.(type) {
	case *ast.UnaryExpr: // &X
		return rhsTypes(a, req, f, imports, e.X, depth, curPkg)
	case *ast.ParenExpr:
		return rhsTypes(a, req, f, imports, e.X, depth, curPkg)
	case *ast.CompositeLit:
		return typeSources(e.Type, imports, curPkg)
	case *ast.CallExpr:
		return callReturnTypes(a, req, f, imports, e.Fun, depth, curPkg)
	case *ast.Ident:
		if depth <= 0 {
			return nil
		}
		return varChainTypes(a, req, f, imports, e.Name, depth-1, curPkg)
	}
	return nil
}

// typeSources 类型表达式 → 候选（Ident 同包；SelectorExpr 跨包——
// import 映射）。
func typeSources(t ast.Expr, imports map[string]string, curPkg string) []writeSource {
	switch e := t.(type) {
	case *ast.Ident:
		return []writeSource{{typeName: e.Name, pkgPath: curPkg}}
	case *ast.SelectorExpr:
		if id, ok := e.X.(*ast.Ident); ok {
			if pkg, ok := imports[id.Name]; ok {
				return []writeSource{{typeName: e.Sel.Name, pkgPath: pkg}}
			}
		}
	}
	return nil
}

// callReturnTypes 构造器调用 → 返回类型来源：同包函数直接 AST 查；
// 跨包（pkg.NewX）经索引节点读源码。
func callReturnTypes(a *Actions, req CodeSequenceRequest, f *ast.File, imports map[string]string, fun ast.Expr, depth int, curPkg string) []writeSource {
	switch e := fun.(type) {
	case *ast.Ident:
		return funcReturnTypes(a, req, f, imports, e.Name, depth, curPkg)
	case *ast.SelectorExpr:
		if id, ok := e.X.(*ast.Ident); ok {
			if pkg, ok := imports[id.Name]; ok {
				return indexedFuncReturnTypes(a, req, pkg, e.Sel.Name, depth)
			}
		}
	}
	return nil
}

// funcReturnTypes 同包函数声明 → 返回类型来源。优先函数体 return RHS
// （构造器"声明返回接口、函数体 return 具体实现"——比返回类型声明更
// 具体）；无 return 再退回返回类型声明。
func funcReturnTypes(a *Actions, req CodeSequenceRequest, f *ast.File, imports map[string]string, name string, depth int, curPkg string) []writeSource {
	var fd *ast.FuncDecl
	ast.Inspect(f, func(node ast.Node) bool {
		if d, ok := node.(*ast.FuncDecl); ok && d.Name.Name == name {
			fd = d
			return false
		}
		return true
	})
	if fd == nil || fd.Body == nil {
		return nil
	}
	var out []writeSource
	for _, st := range fd.Body.List {
		if ret, ok := st.(*ast.ReturnStmt); ok {
			for _, r := range ret.Results {
				out = appendUnique(out, rhsTypes(a, req, f, imports, r, depth, curPkg)...)
			}
		}
	}
	if len(out) > 0 {
		return out
	}
	if fd.Type.Results != nil {
		for _, r := range fd.Type.Results.List {
			out = appendUnique(out, typeSources(r.Type, imports, curPkg)...)
		}
	}
	return out
}

// indexedFuncReturnTypes 跨包构造器：索引节点（GetSymbol）→ 读源码 →
// 同包逻辑（该文件自身的 import 映射）。
func indexedFuncReturnTypes(a *Actions, req CodeSequenceRequest, pkg, name string, depth int) []writeSource {
	n, err := a.repo.GetSymbol(domain.CanonicalID("symbol:go:" + pkg + ":" + name))
	if err != nil || n.FilePath == "" {
		return nil
	}
	src, err := os.ReadFile(filepath.Join(req.RepoAbs, n.FilePath))
	if err != nil {
		return nil
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, n.FilePath, src, 0)
	if err != nil {
		return nil
	}
	return funcReturnTypes(a, req, f, importAliases(f), name, depth, pkg)
}

// varChainTypes 变量链回溯：找 "v := RHS" / "v = RHS" 赋值 → 递归解析
// RHS（多赋值处都收集——条件分支形态）。
func varChainTypes(a *Actions, req CodeSequenceRequest, f *ast.File, imports map[string]string, name string, depth int, curPkg string) []writeSource {
	var out []writeSource
	ast.Inspect(f, func(node ast.Node) bool {
		as, ok := node.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		id, ok := as.Lhs[0].(*ast.Ident)
		if !ok || id.Name != name {
			return true
		}
		out = appendUnique(out, rhsTypes(a, req, f, imports, as.Rhs[0], depth, curPkg)...)
		return true
	})
	return out
}

// appendUnique 去重追加（同 typeName+pkgPath 只保留一个）。
func appendUnique(out []writeSource, more ...writeSource) []writeSource {
	for _, m := range more {
		dup := false
		for _, e := range out {
			if e == m {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, m)
		}
	}
	return out
}
