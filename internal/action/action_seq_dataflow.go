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

// receiverFieldImpl 数据流具体化（R97-2 + P0-3）：receiver 字段
// （s.manager）的赋值来源类型——查摘要 direct_write 写入函数 → 读源码
// AST 找赋值表达式（x.manager = ...）的类型来源（字面量/构造器/变量
// 链/条件分支多候选）→ 构造 (Impl).Method。找不到（无写入摘要/形态
// 不支持）返回空——调用方 fallback 接口类型匹配（InterfaceMethodImpl）。
func (a *Actions) receiverFieldImpl(req CodeSequenceRequest, recvType, field, method string) string {
	if impls := a.receiverFieldImpls(req, recvType, field, method); len(impls) > 0 {
		return impls[0]
	}
	return ""
}

// receiverFieldImpls 字段写入来源的**全部**实现方法（多个写者/多个
// 具体类型 → 调用方必须保留候选，不能取第一个当唯一证据）。
func (a *Actions) receiverFieldImpls(req CodeSequenceRequest, recvType, field, method string) []string {
	key := "." + recvType + "." + field
	writers, cached := req.fieldWriters[key]
	if !cached {
		var err error
		writers, err = a.repo.GetFieldWriters(key)
		if err != nil {
			writers = nil
		}
		if len(writers) == 0 {
			// 索引里的 field_path 是 SSA 的 pkg.Type.field 全路径（真实 init 的
			// 普通形态）；先精确查短路径，未命中再按类型/字段后缀补查。
			if ws, serr := a.repo.GetFieldWritersBySuffix(key); serr == nil {
				writers = ws
			}
		}
		if req.fieldWriters != nil {
			req.fieldWriters[key] = writers // 命中/未命中都缓存（请求内）
		}
	}
	if len(writers) == 0 {
		return nil
	}
	var out []string
	for _, w := range writers {
		for _, c := range a.fieldWriteTypes(req, w, field) {
			id := GrpcMethodEntryID("symbol:go:"+c.pkgPath+":"+c.typeName, method)
			if id == "" {
				continue
			}
			dup := false
			for _, e := range out {
				if e == id {
					dup = true
					break
				}
			}
			if !dup {
				out = append(out, id)
			}
		}
	}
	return out
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
	cacheKey := writerID + "\x00" + field
	if v, ok := req.fieldTypes[cacheKey]; ok {
		return v
	}
	n, err := a.repo.GetSymbol(domain.CanonicalID(writerID))
	if err != nil || n.FilePath == "" {
		if req.fieldTypes != nil {
			req.fieldTypes[cacheKey] = nil
		}
		return nil
	}
	// todo22 P2：共享请求级 AST 缓存（同一写入函数只读盘/解析一次）
	ctx := seqFileCtxFor(req, n.FilePath)
	if ctx == nil {
		if req.fieldTypes != nil {
			req.fieldTypes[cacheKey] = nil
		}
		return nil
	}
	f := ctx.file

	curPkg := pkgPathOf(writerID)
	imports := importAliases(f)
	var out []writeSource
	ast.Inspect(f, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.AssignStmt:
			if len(n.Lhs) != 1 || len(n.Rhs) != 1 {
				return true
			}
			sel, ok := n.Lhs[0].(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != field {
				return true
			}
			out = appendUnique(out, rhsTypes(a, req, f, imports, n.Rhs[0], 3, curPkg)...)
			return true
		case *ast.CompositeLit:
			// todo24 §11.2：构造器里 `&Service{Biz: Bridge()}` 也是字段写入
			// （此前只认 `x.Biz = ...` 赋值，接口字段永远只能走接口候选）。
			for _, el := range n.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok || key.Name != field {
					continue
				}
				out = appendUnique(out, rhsTypes(a, req, f, imports, kv.Value, 3, curPkg)...)
			}
		}
		return true
	})
	if req.fieldTypes != nil {
		req.fieldTypes[cacheKey] = out
	}
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
