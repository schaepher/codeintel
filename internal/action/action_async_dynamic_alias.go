package action

// P1-01：动态别名（reflect 取值 / 接口类型断言）识别。
//
// 这类形态的实例身份只能在运行期确定——静态分析能做的只有"定位 + 说明"，
// 不能升级为 corroborated（todo18：前提缺失就降级并指出缺口）。

import (
	"go/ast"
)

// dynamicAliasReason 识别动态别名形态（reflect / 接口类型断言）。
//
// 返回 (kind, line, true)：kind 为 "reflect" 或 "assert"。
// 只识别**影响实例身份**的形态：reflect.Value 取值（ValueOf/Value.Elem/
// Interface）、以及对接口值做类型断言（v.(*Bus)）。
func dynamicAliasReason(pf *parsedFunc, e ast.Expr) (string, int, bool) {
	if e == nil {
		return "", 0, false
	}
	var kind string
	line := 0
	ast.Inspect(e, func(n ast.Node) bool {
		if kind == "reflect" {
			return false // reflect 更具体，命中即停
		}
		switch x := n.(type) {
		case *ast.CallExpr:
			sel, ok := x.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok || id.Name != "reflect" {
				return true
			}
			switch sel.Sel.Name {
			case "ValueOf", "New", "Indirect", "Elem", "Interface":
				kind = "reflect"
			}
			if kind != "" && pf != nil && pf.fset != nil {
				line = pf.fset.Position(n.Pos()).Line
			}
		case *ast.TypeAssertExpr:
			// v.(*Bus) 的 v 来自接口/any → 实现身份只能在运行期确定
			if kind == "" {
				if _, ok := x.Type.(*ast.StarExpr); ok {
					kind = "assert"
					if pf != nil && pf.fset != nil {
						line = pf.fset.Position(n.Pos()).Line
					}
				}
			}
		}
		return true
	})
	return kind, line, kind != ""
}

// hasReflectOrAssert 表达式内是否含 reflect 调用或类型断言（不定位）。
func hasReflectOrAssert(e ast.Expr) bool {
	_, _, ok := dynamicAliasReason(nil, e)
	return ok
}

// dynamicAliasText 动态别名的原因说明（含行号）。
func dynamicAliasText(kind string, line int) string {
	at := ""
	if line > 0 {
		at = "（L" + itoaSeq(line) + "）"
	}
	switch kind {
	case "reflect":
		return "动态别名：经 reflect 取值" + at + "，实例身份只能在运行期确定"
	case "assert":
		return "动态别名：经接口类型断言取得" + at + "，实例身份只能在运行期确定"
	}
	return "动态别名" + at
}
