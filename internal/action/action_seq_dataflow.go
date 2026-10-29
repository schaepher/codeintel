package action

import (
	"go/ast"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

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
func receiverFieldSel(sel *ast.SelectorExpr, recvObj *ast.Object, recvName string) (string, bool) {
	x, ok := sel.X.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	base, isID := x.X.(*ast.Ident)
	if !isID {
		return "", false
	}
	// todo23 §3.1：必须是**当前 receiver 对象**，不能只比较变量名/任意
	// `x.field`（局部变量/形参同名字段不能借用 receiver 的写入证明）。
	if recvObj != nil {
		if base.Obj != recvObj {
			return "", false
		}
	} else if recvName != "" {
		if base.Name != recvName {
			return "", false
		}
	} else {
		return "", false
	}
	return x.Sel.Name, true
}

// writeSource 字段赋值来源候选（类型名 + 包路径）——P0-3 扩展。
type writeSource struct {
	typeName string
	pkgPath  string
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
// callReturnTypes 构造器调用 → 返回类型来源：同包函数直接 AST 查；
// 跨包（pkg.NewX）经索引节点读源码。
func callReturnTypes(a *Actions, req CodeSequenceRequest, f *ast.File, imports map[string]string, fun ast.Expr, depth int, curPkg string) []writeSource {
	// 递归构造器（func F() func() { return F() }）必须由深度预算终止——
	// 早前这里不减 depth，遇到自引用工厂会无限递归（栈溢出）。
	if depth <= 0 {
		return nil
	}
	switch e := fun.(type) {
	case *ast.Ident:
		// todo24 §11.2：裸函数名先看 AST 对象——局部函数值/变量/类型遮蔽时
		// 不得回退到同名包函数（更不能用名字猜返回类型）。
		if e.Obj != nil && e.Obj.Kind != ast.Fun {
			return nil
		}
		return funcReturnTypes(a, req, f, imports, e.Name, depth-1, curPkg)
	case *ast.SelectorExpr:
		if id, ok := e.X.(*ast.Ident); ok {
			if pkg, ok := imports[id.Name]; ok {
				return indexedFuncReturnTypes(a, req, pkg, e.Sel.Name, depth-1)
			}
		}
	}
	return nil
}

// indexedFuncReturnTypes 跨包构造器：索引节点（GetSymbol）→ 读源码 →
// 同包逻辑（该文件自身的 import 映射）。
func indexedFuncReturnTypes(a *Actions, req CodeSequenceRequest, pkg, name string, depth int) []writeSource {
	cacheKey := pkg + "\x00" + name + "\x00" + itoaSeq(depth)
	if v, ok := req.funcRets[cacheKey]; ok {
		return v
	}
	n, err := a.repo.GetSymbol(domain.CanonicalID("symbol:go:" + pkg + ":" + name))
	if err != nil || n.FilePath == "" {
		if req.funcRets != nil {
			req.funcRets[cacheKey] = nil
		}
		return nil
	}
	ctx := seqFileCtxFor(req, n.FilePath)
	if ctx == nil {
		if req.funcRets != nil {
			req.funcRets[cacheKey] = nil
		}
		return nil
	}
	out := funcReturnTypes(a, req, ctx.file, importAliases(ctx.file), name, depth, pkg)
	if req.funcRets != nil {
		req.funcRets[cacheKey] = out
	}
	return out
}
