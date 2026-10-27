package action

// todo22 §2：表达式的函数值求值、形参绑定、赋值/声明记录，以及目标包
// 索引判定（外部库不得用 AST 身份冒充可展开目标）。

import (
	"go/ast"
	"path"
	"strconv"
	"strings"
)

// seqValueOfExpr 把一个表达式求值为函数值（纯语法数据流，不猜类型）：
//   - 函数字面量 → lit
//   - 标识符 → 环境绑定（区分 AST 对象身份）；未绑定的包级函数 → named
//   - 包选择器 pkg.Fn → named（按文件 import 解析包路径）
//   - 复合字面量 → 函数列表（元素逐项求值，顺序保留）
//   - 调用结果 → 按被调函数的 return 表达式解析（工厂返回，todo22 §2.3）
//   - 方法值/其他 → 明确 unknown（不做名称猜测）
func seqValueOfExpr(a *Actions, req CodeSequenceRequest, ctx *seqFileCtx, env *seqEnv, expr ast.Expr) seqValue {
	return seqValueOfExprDepth(a, req, ctx, env, expr, 0)
}

// seqValueOfExprDepth 带深度上限的求值（工厂返回可能多级，防递归）。
func seqValueOfExprDepth(a *Actions, req CodeSequenceRequest, ctx *seqFileCtx, env *seqEnv, expr ast.Expr, depth int) seqValue {
	switch e := expr.(type) {
	case *ast.FuncLit:
		return literalSeqValue(req, ctx, env, e, seqOriginVar)
	case *ast.Ident:
		if env != nil {
			if v, ok := env.lookup(e.Obj); ok {
				return v
			}
		}
		if e.Obj != nil && e.Obj.Kind == ast.Fun {
			return namedSeqValue("symbol:go:" + pkgPathOf(req.curSym) + ":" + e.Name)
		}
		return unknownSeqValue("标识符 " + e.Name + " 未绑定到函数值（外部调用/未解析的赋值）")
	case *ast.SelectorExpr:
		if id, ok := e.X.(*ast.Ident); ok && (id.Obj == nil || id.Obj.Kind == ast.Pkg) {
			if p := seqImportPath(ctx.file, id.Name); p != "" {
				return namedSeqValue("symbol:go:" + p + ":" + e.Sel.Name)
			}
		}
		return unknownSeqValue("选择器表达式未解析（方法值/字段 " + seqExprText(ctx.fset, ctx.src, e) + "）")
	case *ast.CallExpr:
		// todo22 §2.4：内建 append(列表, 元素…) 扩展函数列表
		if id, ok := e.Fun.(*ast.Ident); ok && id.Name == "append" && len(e.Args) >= 1 {
			base := seqValueOfExprDepth(a, req, ctx, env, e.Args[0], depth)
			if base.kind == seqValueList {
				elems := append([]seqValue(nil), base.elems...)
				for _, arg := range e.Args[1:] {
					v := seqValueOfExprDepth(a, req, ctx, env, arg, depth)
					v.origin = seqOriginVar
					elems = append(elems, v)
				}
				lv := listSeqValue(elems)
				if len(elems) > seqMaxListItems {
					lv.why = "函数列表超过元素预算 " + strconv.Itoa(seqMaxListItems) + "（todo22 §2.4：不截取前几项冒充完整）"
				}
				return lv
			}
			return unknownSeqValue("append 的基础列表不是已知函数列表（todo22 §2.4）")
		}
		// todo22 §2.3：工厂返回——按被调函数的 return 表达式解析（多 return
		// 路径 → 未消歧多候选；不按函数名猜返回类型）
		if depth >= seqMaxFactoryDepth {
			return unknownSeqValue("工厂返回解析超过深度上限")
		}
		if vals, ok := seqCalleeReturnValues(a, req, ctx, e, depth); ok {
			return multiSeqValue(vals, "工厂有多条 return 路径（条件返回未消歧）")
		}
		return unknownSeqValue("调用结果未解析为函数值（非本仓工厂/方法值/外部函数）")
	case *ast.CompositeLit:
		// todo22 §2.4：[]func(){...}（0 个元素 = 确定的空列表）
		elems := make([]seqValue, 0, len(e.Elts))
		for _, el := range e.Elts {
			v := seqValueOfExprDepth(a, req, ctx, env, el, depth)
			v.origin = seqOriginVar
			elems = append(elems, v)
		}
		lv := listSeqValue(elems)
		if len(elems) > seqMaxListItems {
			lv.why = "函数列表超过元素预算 " + strconv.Itoa(seqMaxListItems) + "（todo22 §2.4：不截取前几项冒充完整）"
		}
		return lv
	case nil:
		return unknownSeqValue("空表达式")
	default:
		return unknownSeqValue("不支持的函数值表达式: " + seqExprText(ctx.fset, ctx.src, expr))
	}
}

// seqImportPath 文件里包名 → import path（默认包名取路径末段；支持别名）。
func seqImportPath(f *ast.File, name string) string {
	if f == nil {
		return ""
	}
	for _, imp := range f.Imports {
		p := strings.Trim(imp.Path.Value, "\"`")
		local := path.Base(p)
		if imp.Name != nil {
			local = imp.Name.Name
		}
		if local == name {
			return p
		}
	}
	return ""
}

// bindSeqParams 按位置把实参值绑到形参（多重名/无名形参都按位置消耗）。
// 没有实参绑定的形参 = 明确的 unknown（入口函数/外部调用者）。
func bindSeqParams(env *seqEnv, params *ast.FieldList, values []seqValue) {
	if env == nil || params == nil {
		return
	}
	i := 0
	for _, field := range params.List {
		names := field.Names
		if len(names) == 0 {
			i++
			continue
		}
		// todo22 §2.4：变参形参（...T）把剩余实参收成一个列表值；没有实参
		// = 确定的空列表（不是 unknown）。
		if _, variadic := field.Type.(*ast.Ellipsis); variadic {
			var elems []seqValue
			if i < len(values) {
				elems = append(elems, values[i:]...)
			}
			name := names[len(names)-1]
			if name.Obj != nil {
				if len(elems) > seqMaxListItems {
					env.set(name.Obj, unknownSeqValue("变参列表超过元素预算 "+strconv.Itoa(seqMaxListItems)))
				} else {
					env.set(name.Obj, listSeqValue(elems))
				}
			}
			return
		}
		for _, name := range names {
			if i < len(values) {
				v := values[i]
				v.origin = seqOriginArg
				v.declObj = name.Obj
				env.set(name.Obj, v)
			} else {
				env.set(name.Obj, unknownSeqValue("形参 "+name.Name+" 无实参绑定（入口函数或外部调用者）"))
			}
			i++
		}
	}
}
