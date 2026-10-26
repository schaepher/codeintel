package action

import (
	"go/ast"
	"strings"
)

// effectErrorSources 每条返回槽的错误来源（todo12-8 R3 / todo13-8）：
//   - returned：返回当前错误变量（含显式检查后返回）
//   - wrapped：返回表达式包裹了错误值
//   - swallowed：错误变量非 nil 后被 nil 赋值/最终返回 nil
//   - overwritten：返回的不是最近的错误值（被后续赋值覆盖）
//   - stale：返回的名字不是最近的错误来源
//   - nil / unknown：无错误或无法判定
func effectErrorSources(sp pathSpec, resultNames, resultTypes, slots []string) []ErrorSource {
	if sp.returnLine == 0 {
		return nil
	}
	var out []ErrorSource
	for i, expr := range sp.returnVals {
		slot := i
		if i < len(slots) {

			if n, ok := slotIndex(slots[i]); ok {
				slot = n
			}
		}
		if !errorSlot(slot, resultNames, resultTypes) {
			continue
		}
		out = append(out, effectErrorSource(sp, slot, expr, sp.returnExprs[i]))
	}
	if len(sp.returnVals) == 0 && len(resultNames) > 0 {

		for i := range resultNames {
			if i < len(resultNames) && errorSlot(i, resultNames, resultTypes) {
				out = append(out, effectErrorSource(sp, i, resultNames[i], nil))
			}
		}
	}
	return out
}

// errorSlot 返回槽是否是 error（todo13-8：按类型判定，不靠变量名/末槽
// 猜测——`(approval, audit string)` 的 audit 不是错误槽）。
func errorSlot(i int, resultNames, resultTypes []string) bool {
	if i >= 0 && i < len(resultTypes) {
		return strings.Contains(strings.ToLower(resultTypes[i]), "error")
	}
	if i < len(resultNames) {
		return errLikeName(resultNames[i])
	}
	return false
}

// resultTypeNames 返回槽类型短名（与槽顺序对齐）。
func resultTypeNames(fl *ast.FieldList) []string {
	if fl == nil {
		return nil
	}
	var out []string
	for _, f := range fl.List {
		typ := starTypeName(f.Type)
		n := len(f.Names)
		if n == 0 {
			n = 1
		}
		for i := 0; i < n; i++ {
			out = append(out, typ)
		}
	}
	return out
}

// slotIndex "#1" → 1（非槽形态返回 false）。
func slotIndex(s string) (int, bool) {
	if len(s) < 2 || s[0] != '#' {
		return 0, false
	}
	n := 0
	for _, r := range s[1:] {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}

// effectErrorSource 单槽判定（沿路径的局部变量赋值序列）。
//
// todo13-8：不只按变量名匹配——RHS 是错误构造/被调错误返回、或该槽本身
// 是错误槽（末槽/err 类名）时，按**定义/赋值顺序**（值身份）分析；变量
// 重命名不改变结论。return 表达式直接调用 → returned_call（被调返回错误）。
func effectErrorSource(sp pathSpec, slot int, expr string, returnExpr ast.Expr) ErrorSource {
	es := ErrorSource{Slot: slot, Expr: strings.TrimSpace(expr), Status: "unknown"}
	// 返回表达式直接调用：错误来源是被调返回值（调用位置可定位）
	if call, ok := returnExpr.(*ast.CallExpr); ok {
		es.Status = "returned_call"
		es.Line = sp.returnLine
		es.Detail = "返回被调函数的错误值（" + callName(call) + "；不推断回滚）"
		return es
	}
	// 收集候选错误变量赋值（按路径顺序）：
	//   - 名字像错误；或
	//   - RHS 是错误构造/错误返回调用；或
	//   - 是本次返回表达式引用的变量（错误槽的返回值）
	wantVar := strings.TrimSpace(es.Expr)
	type ev struct {
		name string
		line int
		nil  bool
		call bool
	}
	var evs []ev
	for _, la := range sp.locals {
		if !errLikeName(la.name) && !rhsLooksLikeErrorCall(la.rhs) && la.name != wantVar {
			continue
		}
		nilRHS := isNilIdent(la.rhs)
		evs = append(evs, ev{name: la.name, line: la.line, nil: nilRHS,
			call: rhsLooksLikeErrorCall(la.rhs)})
	}
	if len(evs) == 0 {
		if es.Expr == "nil" {
			es.Status = "nil"
			es.Detail = "该路径返回 nil（无错误变量赋值）"
		}
		return es
	}
	last := evs[len(evs)-1]

	lastNonNil := ev{}
	for _, e := range evs {
		if !e.nil {
			lastNonNil = e
		}
	}
	if es.Expr == "nil" {

		for _, e := range evs {
			if errorVarKnownNil(sp.conditions, e.name) {
				es.Status = "nil"
				es.Detail = "该分支条件下错误变量已为 nil"
				return es
			}
		}
		if lastNonNil.name != "" {
			es.Status, es.Var, es.Line = "swallowed", lastNonNil.name, lastNonNil.line
			es.OverwrittenAt = last.line
			es.Detail = "错误值未返回（最终返回 nil；不推断事务回滚）"
		} else {
			es.Status = "nil"
		}
		return es
	}
	exprName := strings.TrimSpace(es.Expr)
	if errLikeName(exprName) || wantVar == exprName {
		es.Var = exprName
		// 该变量在返回前的最后一次赋值
		var mine, mineNonNil ev
		for _, e := range evs {
			if e.name != exprName {
				continue
			}
			mine = e
			if !e.nil {
				mineNonNil = e
			}
		}
		if mine.name == "" {
			es.Status = "unknown"
			es.Detail = "返回的错误变量在路径上没有赋值（形参/外部输入）"
			return es
		}
		es.Line = mine.line
		if mine.nil {
			es.Status = "swallowed"
			es.OverwrittenAt = mine.line
			es.Detail = "返回的错误变量最终被赋值为 nil（原非 nil 错误被覆盖；错误被吞）"
			return es
		}
		if lastNonNil.name != "" && lastNonNil.name != exprName && lastNonNil.line > mine.line {
			es.Status = "stale"
			es.Detail = "返回的 " + exprName + " 不是最近的错误来源（" +
				lastNonNil.name + " 在第 " + itoaSeq(lastNonNil.line) + " 行）"
			return es
		}
		if last.line > mineNonNil.line && last.name == exprName && last.nil {
			es.Status = "overwritten"
			es.OverwrittenAt = last.line
			es.Detail = "返回前该错误被重新赋值（覆盖）"
			return es
		}
		es.Status = "returned"
		es.Detail = "返回路径上的错误变量"
		return es
	}

	for _, e := range evs {
		if e.name != "" && regexpContainsWord(exprName, e.name) {
			es.Status, es.Var, es.Line = "wrapped", e.name, e.line
			es.Detail = "返回表达式包装了错误变量"
			return es
		}
	}
	return es
}

// rhsLooksLikeErrorCall RHS 是否明显产生 error（errors.New / fmt.Errorf /
// *Error 构造 / 名为 Error/Wrap 的调用）——用于类型信息缺失时的保守识别。
func rhsLooksLikeErrorCall(rhs []ast.Expr) bool {
	if len(rhs) != 1 {
		return false
	}
	call, ok := rhs[0].(*ast.CallExpr)
	if !ok {
		return false
	}
	name := strings.ToLower(callName(call))
	if strings.Contains(name, "error") || strings.HasPrefix(name, "wrap") {
		return true
	}
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		if id, ok := sel.X.(*ast.Ident); ok {
			pkg := strings.ToLower(id.Name)
			if (pkg == "errors" && (name == "new" || name == "unwrap")) ||
				(pkg == "fmt" && name == "errorf") {
				return true
			}
		}
	}
	return false
}

// errorVarKnownNil 路径条件是否已证明错误变量为 nil。
func errorVarKnownNil(conditions []string, name string) bool {
	if name == "" {
		return false
	}
	for _, c := range conditions {
		c = strings.ReplaceAll(c, " ", "")
		if c == "!("+name+"!=nil)" || c == name+"==nil" || c == "!("+name+"==nil)!" {
			return true
		}
	}
	return false
}

// regexpContainsWord 标识符是否出现在表达式文本里（词边界近似）。
func regexpContainsWord(text, word string) bool {
	idx := 0
	for {
		i := strings.Index(text[idx:], word)
		if i < 0 {
			return false
		}
		i += idx
		leftOK := i == 0 || !isIdentChar(text[i-1])
		right := i + len(word)
		rightOK := right >= len(text) || !isIdentChar(text[right])
		if leftOK && rightOK {
			return true
		}
		idx = i + len(word)
	}
}
func isIdentChar(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}
