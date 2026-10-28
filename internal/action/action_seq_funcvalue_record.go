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
		v := captureSeqClosureValue(a, req, ctx, s.Rhs[i])
		seqStampBinding(&v, id.Obj, ctx, s.Pos(), req.branchCond)
		req.env.set(id.Obj, v)
		if ref, ok := seqTypeOfExpr(a, req, ctx, s.Rhs[i]); ok {
			req.env.types[id.Obj] = ref
		} else {
			delete(req.env.types, id.Obj) // 明确未知：不沿用旧的类型绑定
		}
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
				v := captureSeqClosureValue(a, req, ctx, vs.Values[i])
				seqStampBinding(&v, name.Obj, ctx, vs.Pos(), req.branchCond)
				req.env.set(name.Obj, v)
				if ref, ok := seqTypeOfExpr(a, req, ctx, vs.Values[i]); ok {
					req.env.types[name.Obj] = ref
				}
				continue
			}
			// todo22 §2.4：`var list []func()` 是**确定的空列表**（nil slice
			// range 零次）；只有元素类型明确是 func 时才这么判。
			if seqEmptyFuncSliceTypeIn(a, req, ctx, vs.Type) {
				req.env.set(name.Obj, listSeqValue(nil))
				continue
			}
			// todo22 P1：`var s ext.Store` 的**声明类型**也要记录（外部包
			// 方法调用/字段类型解析依赖它）。
			if vs.Type != nil {
				if ref, ok := seqTypeRefOf(req, ctx, vs.Type); ok {
					req.env.types[name.Obj] = ref
				}
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
			label := c.label()
			if c.kind == seqValueNamed {
				label = c.symID
			}
			// todo22 §2.1/§2.3：候选要带"在哪个条件下成立"
			if c.cond != "" {
				label += " (when " + c.cond + ")"
			}
			out = append(out, label)
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

// seqStampBinding 给值盖上绑定元数据（声明对象/调用上下文/条件），供证据与
// 诊断使用（todo22 §2.1：不能只保留"某个值"，要能说清它绑在谁身上）。
func seqStampBinding(v *seqValue, obj *ast.Object, ctx *seqFileCtx, pos token.Pos, cond string) {
	if v == nil {
		return
	}
	v.declObj = obj
	if ctx != nil {
		v.bindFile = ctx.rel
	}
	v.bindLine = lineOfOffset(ctx, pos)
	v.cond = cond
	// 统一把元数据也盖到候选上（多候选时每个候选各自的条件才有意义）
	for i := range v.cands {
		if v.cands[i].cond == "" {
			v.cands[i].cond = cond
		}
	}
}

// seqRecordParamTypes 记录形参的声明类型（局部变量之外的 `p.Method()` 解析）。
func seqRecordParamTypes(req CodeSequenceRequest, env *seqEnv, params *ast.FieldList) {
	if env == nil || params == nil || req.curCtx == nil {
		return
	}
	for _, f := range params.List {
		ref, ok := seqTypeRefOf(req, req.curCtx, f.Type)
		if !ok {
			continue
		}
		for _, n := range f.Names {
			if n.Obj != nil {
				env.types[n.Obj] = ref
			}
		}
	}
}
