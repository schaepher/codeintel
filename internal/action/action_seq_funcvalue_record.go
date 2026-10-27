package action

// todo22 §2：赋值/声明的函数值记录（RHS 先求值、LHS 从下一条语句起生效）。

import (
	"go/ast"
	"go/token"
	"strconv"

	"github.com/schaepher/codeintel/internal/domain"
)

// recordSeqAssign 记录赋值里的函数值（在调用节点生成**之后**执行：RHS 读取
// 的是赋值前的值，LHS 从下一条语句起生效）。
func recordSeqAssign(a *Actions, req CodeSequenceRequest, ctx *seqFileCtx, s *ast.AssignStmt) {
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
		req.env.set(id.Obj, seqValueOfExpr(a, req, ctx, req.env, s.Rhs[i]))
	}
}

// recordSeqDecl 记录 `var f = func(){…}` / `var f func()`。
func recordSeqDecl(a *Actions, req CodeSequenceRequest, ctx *seqFileCtx, gd *ast.GenDecl) {
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
				req.env.set(name.Obj, seqValueOfExpr(a, req, ctx, req.env, vs.Values[i]))
				continue
			}
			// todo22 §2.4：`var list []func()` 是**确定的空列表**（nil slice
			// range 零次）；只有元素类型明确是 func 时才这么判。
			if seqEmptyFuncSliceType(vs.Type) {
				req.env.set(name.Obj, listSeqValue(nil))
				continue
			}
			req.env.set(name.Obj, unknownSeqValue("var 声明无初始值: "+name.Name))
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

// seqEmptyFuncSliceType 声明类型是否是 []func(...)（nil slice = 确定空列表）。
func seqEmptyFuncSliceType(t ast.Expr) bool {
	at, ok := t.(*ast.ArrayType)
	if !ok || at.Elt == nil {
		return false
	}
	_, isFunc := at.Elt.(*ast.FuncType)
	return isFunc
}
