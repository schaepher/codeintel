package action

// todo12-1：`query effects` 的目标筛选（按字段类型身份 / 返回槽）。
//
// 用户确认的契约（R1–R6）：
//   - --target-field <类型限定字段路径> / --target-return <名字|#槽位> 互斥
//   - 字段按**类型身份**解析（pkg.Type.Field），同名不同结构体不混用；
//     返回槽按签名解析（#0 起），筛选后不重编号
//   - 目标筛选只裁剪写入/返回与调用，不改变路径可达性；预算/未知/截断
//     进 analysis.status=partial，不静默 complete
//
// 依赖口径：值来源只沿当前路径（局部变量赋值 + 返回表达式 + 字段写入的
// 右值），不跨函数找同名变量；未知类型保守保留并标 partial。

import (
	"go/ast"
	"strconv"
	"strings"
)

// EffectsTarget 目标筛选的结果（JSON `target`）。

// EffectsAnalysis 目标模式的分析完整性（R5；未知/截断 → partial）。

// EffectsPathBudget 路径预算截断的可核对信息。

// 目标模式诊断代码。

// effectsTarget 已解析的目标（内部）。

// resolveEffectsTarget 解析目标参数（空值/不存在/歧义/越界均报错）。

// splitFieldTargetRef "pkg/path.Type.Field" / "Type.Field" → (类型引用, 字段)。

// resolveStructNode 类型引用 → 结构体节点（完整路径优先；短名须唯一）。

// typeIdentity 结构体节点 → pkg.Type（字段身份前缀）。

// structHasField 结构体是否有该字段（verifiable=false 表示无字段元数据）。

// structFieldNames 字段清单（错误提示用）。

// parseReturnSlot 名字或 #槽位 → 下标（空值/不存在/越界报错）。

// writeIdentity AST 字段表达式 → 类型身份（known=false 表示类型不可判定）。

// keepFieldWrite 字段目标：类型身份精确匹配；类型未知时同名保守保留。
func (t *effectsTarget) keepFieldWrite(id string, known bool, leaf string) (keep, unknown bool) {
	if known {
		return id == t.fieldID, false
	}
	if leaf == t.fieldName {
		t.unknown++
		return true, true
	}
	return false, false
}

// returnDeps 返回槽的值依赖（局部变量名 + 字段身份）。
type returnDeps struct {
	locals map[string]bool
	fields map[string]bool
}

// depsForReturn 计算该路径目标返回槽的依赖（不跨函数）。
func (t *effectsTarget) depsForReturn(sp pathSpec) returnDeps {
	d := returnDeps{locals: map[string]bool{}, fields: map[string]bool{}}
	var addExpr func(e ast.Expr, depth int)
	addExpr = func(e ast.Expr, depth int) {
		if e == nil || depth > 3 {
			return
		}
		switch x := e.(type) {
		case *ast.Ident:
			d.locals[x.Name] = true
			for _, la := range sp.locals {
				if la.name != x.Name {
					continue
				}
				for _, r := range la.rhs {
					addExpr(r, depth+1)
				}
			}
		case *ast.SelectorExpr:
			if id, known := writeIdentity(exprTextShallow(x), t.varTypes, t.pkgPath); known {
				d.fields[id] = true
			}
		case *ast.CallExpr:
			for _, a := range x.Args {
				addExpr(a, depth+1)
			}
		case *ast.BinaryExpr:
			addExpr(x.X, depth+1)
			addExpr(x.Y, depth+1)
		case *ast.UnaryExpr:
			addExpr(x.X, depth+1)
		case *ast.ParenExpr:
			addExpr(x.X, depth+1)
		case *ast.StarExpr:
			addExpr(x.X, depth+1)
		case *ast.IndexExpr:
			addExpr(x.X, depth+1)
			addExpr(x.Index, depth+1)
		}
	}
	if t.slot < len(sp.returnExprs) {
		addExpr(sp.returnExprs[t.slot], 0)
	} else if len(sp.returnExprs) == 0 && t.slot < len(t.resultName) && t.resultName[t.slot] != "" {
		// 裸 return：值是具名结果变量本身
		addExpr(&ast.Ident{Name: t.resultName[t.slot]}, 0)
	}
	return d
}

// keepWriteForReturn 返回槽目标：保留影响目标返回值的写入。
func (t *effectsTarget) keepWriteForReturn(ws dbWriteSite, sp pathSpec) bool {
	d := t.depsForReturn(sp)
	if len(d.locals) == 0 && len(d.fields) == 0 {
		return false
	}
	if id, known := writeIdentity(ws.field, t.varTypes, t.pkgPath); known && d.fields[id] {
		return true
	}
	for _, r := range ws.rhs {
		if exprUsesDeps(r, d) {
			return true
		}
	}
	return false
}

// exprUsesDeps 表达式是否引用依赖（局部变量/字段）。
func exprUsesDeps(e ast.Expr, d returnDeps) bool {
	if e == nil {
		return false
	}
	used := false
	ast.Inspect(e, func(n ast.Node) bool {
		if used {
			return false
		}
		switch x := n.(type) {
		case *ast.Ident:
			if d.locals[x.Name] {
				used = true
				return false
			}
		case *ast.SelectorExpr:
			// 只比较文本末段之外的身份由调用方保证；此处按浅文本匹配字段名
			text := exprTextShallow(x)
			for id := range d.fields {
				if strings.HasSuffix(id, "."+text) || strings.HasSuffix(id, text) {
					used = true
					return false
				}
			}
		}
		return true
	})
	return used
}

// filterPathResult 目标模式：只保留与目标相关的返回槽（不重编号）。
func (t *effectsTarget) filterPathResult(sp pathSpec, names []string) *ResultEffect {
	if sp.returnLine == 0 {
		return nil
	}
	if t.kind == "return" {
		res := &ResultEffect{Line: sp.returnLine, Verdict: SeqVerdictCorroborated,
			Slots: []string{"#" + strconv.Itoa(t.slot)}, Detail: "返回值来自该路径的 return"}
		if t.slot < len(sp.returnExprs) {
			res.Returns = []string{sp.returnVals[t.slot]}
			return res
		}
		if len(sp.returnExprs) == 0 && t.slot < len(t.resultName) && t.resultName[t.slot] != "" {
			res.Returns = []string{t.resultName[t.slot]}
			res.Detail = "裸 return：返回值为具名结果变量"
			return res
		}
		t.slotMiss++
		res.Verdict = SeqVerdictUnverified
		res.Detail = "该路径的 return 未提供目标槽（多值调用/表达式个数不足，未做猜测）"
		return res
	}
	// 字段目标：保留依赖目标字段的返回槽
	var returns, slots []string
	for i := range sp.returnExprs {
		if !t.exprDependsOnField(sp.returnExprs[i], sp, 0) {
			continue
		}
		returns = append(returns, sp.returnVals[i])
		slots = append(slots, "#"+strconv.Itoa(i))
	}
	if len(returns) == 0 {
		return nil
	}
	return &ResultEffect{Returns: returns, Slots: slots, Line: sp.returnLine,
		Verdict: SeqVerdictCorroborated, Detail: "返回表达式依赖目标字段"}
}

// exprDependsOnField 表达式是否依赖目标字段（沿当前路径的局部赋值/字段写入）。
func (t *effectsTarget) exprDependsOnField(e ast.Expr, sp pathSpec, depth int) bool {
	if e == nil || depth > 3 {
		return false
	}
	switch x := e.(type) {
	case *ast.SelectorExpr:
		if id, known := writeIdentity(exprTextShallow(x), t.varTypes, t.pkgPath); known {
			if id == t.fieldID {
				return true
			}
		}
		// 该字段被写入时右值依赖目标字段（如 h.State = status，status 来自目标）
		text := exprTextShallow(x)
		for _, ws := range sp.writes {
			if ws.field != text {
				continue
			}
			for _, r := range ws.rhs {
				if t.exprDependsOnField(r, sp, depth+1) {
					return true
				}
			}
		}
	case *ast.Ident:
		for _, la := range sp.locals {
			if la.name != x.Name {
				continue
			}
			for _, r := range la.rhs {
				if t.exprDependsOnField(r, sp, depth+1) {
					return true
				}
			}
		}
	case *ast.CallExpr:
		for _, a := range x.Args {
			if t.exprDependsOnField(a, sp, depth+1) {
				return true
			}
		}
	case *ast.BinaryExpr:
		return t.exprDependsOnField(x.X, sp, depth+1) || t.exprDependsOnField(x.Y, sp, depth+1)
	case *ast.UnaryExpr:
		return t.exprDependsOnField(x.X, sp, depth+1)
	case *ast.ParenExpr:
		return t.exprDependsOnField(x.X, sp, depth+1)
	case *ast.StarExpr:
		return t.exprDependsOnField(x.X, sp, depth+1)
	case *ast.IndexExpr:
		return t.exprDependsOnField(x.X, sp, depth+1) || t.exprDependsOnField(x.Index, sp, depth+1)
	}
	return false
}
