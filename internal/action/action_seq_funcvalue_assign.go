package action

// todo22 §2：表达式的函数值求值、形参绑定、赋值/声明记录，以及目标包
// 索引判定（外部库不得用 AST 身份冒充可展开目标）。

import (
	"go/ast"
	"go/token"
	"path"
	"strconv"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// seqValueOfExpr 把一个表达式求值为函数值（纯语法数据流，不猜类型）：
//   - 函数字面量 → lit
//   - 标识符 → 环境绑定（区分 AST 对象身份）；未绑定的包级函数 → named
//   - 包选择器 pkg.Fn → named（按文件 import 解析包路径）
//   - 调用结果（工厂返回）/方法值/其他 → 明确 unknown（不做名称猜测）
func seqValueOfExpr(req CodeSequenceRequest, ctx *seqFileCtx, env *seqEnv, expr ast.Expr) seqValue {
	switch e := expr.(type) {
	case *ast.FuncLit:
		return literalSeqValue(req, ctx, env, e, req.curTgts, seqOriginVar)
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
		return unknownSeqValue("调用结果未解析为函数值（工厂返回/包装器—todo22 §2.3）")
	case *ast.CompositeLit:
		return unknownSeqValue("复合字面量（函数列表）未展开—todo22 §2.4")
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

// seqBindCallArgs 把调用实参求值为位置参数值（形参绑定用）。origin 记为 arg
// ——展开时的证据是"回调绑定"。
func seqBindCallArgs(req CodeSequenceRequest, ctx *seqFileCtx, call *ast.CallExpr) []seqValue {
	if call == nil {
		return nil
	}
	out := make([]seqValue, 0, len(call.Args))
	for _, arg := range call.Args {
		v := seqValueOfExpr(req, ctx, req.env, arg)
		v.origin = seqOriginArg
		out = append(out, v)
	}
	return out
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
		for _, name := range names {
			if i < len(values) {
				v := values[i]
				v.origin = seqOriginArg
				env.set(name.Obj, v)
			} else {
				env.set(name.Obj, unknownSeqValue("形参 "+name.Name+" 无实参绑定（入口函数或外部调用者）"))
			}
			i++
		}
	}
}

// recordSeqAssign 记录赋值里的函数值（在调用节点生成**之后**执行：RHS 读取
// 的是赋值前的值，LHS 从下一条语句起生效）。
func recordSeqAssign(req CodeSequenceRequest, ctx *seqFileCtx, s *ast.AssignStmt) {
	if req.env == nil || s == nil {
		return
	}
	if len(s.Rhs) != len(s.Lhs) {
		for _, lhs := range s.Lhs {
			if id, ok := lhs.(*ast.Ident); ok && id.Obj != nil {
				req.env.set(id.Obj, unknownSeqValue("多重赋值/多返回值未解析为函数值（todo22 §2）"))
			}
		}
		return
	}
	for i, lhs := range s.Lhs {
		id, ok := lhs.(*ast.Ident)
		if !ok || id.Obj == nil {
			continue
		}
		req.env.set(id.Obj, seqValueOfExpr(req, ctx, req.env, s.Rhs[i]))
	}
}

// recordSeqDecl 记录 `var f = func(){…}` / `var f func()`。
func recordSeqDecl(req CodeSequenceRequest, ctx *seqFileCtx, gd *ast.GenDecl) {
	if req.env == nil || gd == nil || gd.Tok != token.VAR {
		return
	}
	for _, spec := range gd.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for i, name := range vs.Names {
			if name.Obj == nil {
				continue
			}
			if i < len(vs.Values) {
				req.env.set(name.Obj, seqValueOfExpr(req, ctx, req.env, vs.Values[i]))
			} else {
				req.env.set(name.Obj, unknownSeqValue("var 声明无初始值: "+name.Name))
			}
		}
	}
}

// seqValueCandidates 多值候选列表（证据里列出候选身份）。
func seqValueCandidates(v seqValue) []string {
	switch v.kind {
	case seqValueMulti:
		out := make([]string, 0, len(v.cands))
		for _, c := range v.cands {
			if c.kind == seqValueNamed {
				out = append(out, c.symID)
			} else {
				out = append(out, c.label())
			}
		}
		return out
	case seqValueNamed:
		return []string{v.symID}
	}
	return nil
}

// seqLitKey 字面量的稳定身份（防环 + 证据定位）：文件 + 起始偏移。
func seqLitKey(v seqValue) string {
	if v.ctx == nil || v.ctx.fset == nil || v.lit == nil {
		return ""
	}
	pos := v.ctx.fset.Position(v.lit.Pos())
	return v.ctx.rel + ":" + strconv.Itoa(pos.Offset)
}

// seqExprFuncTyped 表达式是否**声明为**函数类型（纯语法：FuncLit 或形参/
// 局部变量的 type 是 func(...)）。用于逃逸判定——未知值也要能识别"它确实
// 是函数值，只是不知道是哪一个"。
func seqExprFuncTyped(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.FuncLit:
		return true
	case *ast.Ident:
		if e.Obj == nil {
			return false
		}
		switch d := e.Obj.Decl.(type) {
		case *ast.Field:
			return seqIsFuncType(d.Type)
		case *ast.ValueSpec:
			return seqIsFuncType(d.Type)
		case *ast.AssignStmt:
			return false
		}
	}
	return false
}

// seqIsFuncType AST 类型表达式是否 func(...)。
func seqIsFuncType(t ast.Expr) bool {
	if t == nil {
		return false
	}
	if ft, ok := t.(*ast.FuncType); ok {
		return ft != nil
	}
	return false
}

// seqTargetIndexed 目标所在包是否已索引——外部库（不在 pkgSet）不得用
// AST 身份恢复冒充"可展开目标"，否则 external_target 会被误报成 index_gap。
// pkgSet 为空（包清单不可用的降级态）时放行，由 codeSeqForSymbol 给出
// 真实 index_gap 诊断。
func seqTargetIndexed(req CodeSequenceRequest, curPkg, symID string) bool {
	pkg := pkgOfID(domain.CanonicalID(symID))
	if pkg == curPkg {
		return true // 同包函数按 AST 对象身份即准确目标
	}
	if len(req.pkgSet) == 0 {
		return false // 包清单不可用（降级态）：只信同包身份，其余保持未解析
	}
	return req.pkgSet[pkg]
}
