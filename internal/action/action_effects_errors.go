package action

import (
	"go/ast"
	"strings"
)

// effectErrorSources 每条返回槽的错误来源（todo12-8 R3）：
//   - returned：返回当前错误变量（含显式检查后返回）
//   - wrapped：返回表达式包裹了错误值
//   - swallowed：错误变量非 nil 后被 nil 赋值/最终返回 nil
//   - overwritten：返回的不是最近的错误值（被后续赋值覆盖）
//   - stale：返回的名字不是最近的错误来源
//   - nil / unknown：无错误或无法判定
func effectErrorSources(sp pathSpec, resultNames, slots []string) []ErrorSource {
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
		out = append(out, effectErrorSource(sp, slot, expr))
	}
	if len(sp.returnVals) == 0 && len(resultNames) > 0 {

		for i := range resultNames {
			if i < len(resultNames) && errLikeName(resultNames[i]) {
				out = append(out, effectErrorSource(sp, i, resultNames[i]))
			}
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

// effectErrorSource 单槽判定（沿路径的局部错误变量赋值序列）。
func effectErrorSource(sp pathSpec, slot int, expr string) ErrorSource {
	es := ErrorSource{Slot: slot, Expr: strings.TrimSpace(expr), Status: "unknown"}
	// 收集错误类局部变量赋值（按路径顺序）
	type ev struct {
		name string
		line int
		nil  bool
	}
	var evs []ev
	for _, la := range sp.locals {
		if !errLikeName(la.name) {
			continue
		}
		nilRHS := false
		if len(la.rhs) == 1 {
			if id, ok := la.rhs[0].(*ast.Ident); ok && id.Name == "nil" {
				nilRHS = true
			}
		}
		evs = append(evs, ev{name: la.name, line: la.line, nil: nilRHS})
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
	if errLikeName(exprName) {
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
			es.Detail = "返回的错误变量最终被赋值为 nil（错误被吞）"
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
