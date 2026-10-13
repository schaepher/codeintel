package ast

// 包级函数值注册表（P2-1 扩展）：识别包级 var 中的具名函数注册
// （如 `var registry = []WrapperValidator{{Validator: ValidateX}}`），
// 并在该变量传入「函数集合入参 + 函数集合返回」的 getter 时，补
// getter → 注册函数 的 CALLS 边（置信度 0.80，默认 callers 可见）。
//
// 函数引用去重并排序（确定性）；类型判定用访问集（见 ast_funcval.go），
// 自引用 struct/slice/map 不会递归爆栈。

import (
	"go/ast"
	"go/token"
	"go/types"
	"sort"

	"golang.org/x/tools/go/packages"
)

// collectPkgFuncSetVars 扫描模块内包的包级 var 声明，返回「函数集合变量
// → 注册函数列表（去重排序）」。只收集初始化表达式里能静态解析的具名
// 函数/方法值（复合字面量元素/嵌套字段）。
func collectPkgFuncSetVars(pkgs []*packages.Package, modules []string) map[*types.Var][]*types.Func {
	out := map[*types.Var][]*types.Func{}
	for _, pkg := range pkgs {
		if pkg == nil || !isInModule(pkg.PkgPath, modules) {
			continue
		}
		for _, f := range pkg.Syntax {
			for _, decl := range f.Decls {
				gd, ok := decl.(*ast.GenDecl)
				if !ok || gd.Tok != token.VAR {
					continue
				}
				for _, spec := range gd.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, name := range vs.Names {
						if i >= len(vs.Values) {
							continue
						}
						v, ok := pkg.TypesInfo.Defs[name].(*types.Var)
						if !ok {
							continue
						}
						if !isFuncSetType(pkg.TypesInfo.TypeOf(vs.Values[i])) {
							continue
						}
						refs := dedupSortFuncs(collectFuncSetRefs(pkg, vs.Values[i]))
						if len(refs) > 0 {
							out[v] = refs
						}
					}
				}
			}
		}
	}
	return out
}

// registryFuncRefs 把实参解析为包级注册表变量并返回其注册函数：
// Ident / SelectorExpr（pkg.Registry）→ *types.Var → 查注册表。
func (ctx *fileCtx) registryFuncRefs(arg ast.Expr) []*types.Func {
	if ctx == nil || ctx.a == nil || len(ctx.a.funcSetVars) == 0 {
		return nil
	}
	var obj types.Object
	switch e := arg.(type) {
	case *ast.Ident:
		obj = ctx.pkg.TypesInfo.Uses[e]
	case *ast.SelectorExpr:
		obj = ctx.pkg.TypesInfo.Uses[e.Sel]
	}
	v, ok := obj.(*types.Var)
	if !ok {
		return nil
	}
	return ctx.a.funcSetVars[v]
}

// dedupSortFuncs 函数引用去重（按完整 ID）并排序（确定性输出）。
func dedupSortFuncs(in []*types.Func) []*types.Func {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]*types.Func, 0, len(in))
	for _, fn := range in {
		if fn == nil || fn.Pkg() == nil {
			continue
		}
		key := fn.Pkg().Path() + "." + fn.Name()
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, fn)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Pkg().Path()+"."+out[i].Name() < out[j].Pkg().Path()+"."+out[j].Name()
	})
	return out
}
