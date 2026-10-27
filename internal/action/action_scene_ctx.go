package action

import (
	"go/ast"
	"strings"
)

// matchContextValueCond 识别 `ctx.Value("key") == nil` / `!= nil` 形态：
// 场景键 = Value 参数的字符串值（字面量/包内常量/跨包常量），场景值
// true/1/on/present → 存在；false/0/off/absent → 不存在。
//
// 这是"运行时事实"的显式输入：没有 --scene 键时不得猜，保持 unknown。
func (s Scene) matchContextValueCond(bin *ast.BinaryExpr, op string, consts sceneConstLookup) (SceneFit, bool) {
	call, ok := sceneNilCompareOperands(bin)
	if !ok {
		return SceneFit{}, false
	}
	key, ok := sceneContextValueKey(call, consts)
	if !ok {
		return SceneFit{Status: SceneUnknown, Field: "ctx.Value(<dynamic>)",
			Reason: "unknown:ctx-key"}, true
	}
	field := `ctx.Value("` + key + `")`
	expected, found := s.contextKeyPresent(key)
	if !found {
		reason := "unknown:no-scene-key"
		if s.Empty() {
			reason = "unknown:no-scene"
		}
		return SceneFit{Status: SceneUnknown, Field: field, Literal: "nil", Reason: reason}, true
	}

	condPresent := op == "!="
	status := SceneExcluded
	if condPresent == expected {
		status = SceneTaken
	}
	exp := "absent"
	if expected {
		exp = "present"
	}
	return SceneFit{Status: status, Key: key, Field: field, Literal: "nil",
		Expected: exp, Reason: "scene-ctx:" + key + "=" + exp, Source: "scene-ctx"}, true
}

// sceneNilCompareOperands 比较两侧恰好一侧是 nil，返回另一侧的调用表达式。
func sceneNilCompareOperands(bin *ast.BinaryExpr) (*ast.CallExpr, bool) {
	isNil := func(e ast.Expr) bool {
		id, ok := e.(*ast.Ident)
		return ok && id.Name == "nil"
	}
	for _, pair := range [][2]ast.Expr{{bin.X, bin.Y}, {bin.Y, bin.X}} {
		if !isNil(pair[1]) {
			continue
		}
		call, ok := pair[0].(*ast.CallExpr)
		if !ok {
			continue
		}
		return call, true
	}
	return nil, false
}

// sceneContextValueKey `x.Value(<key>)` → key 的字符串值（字面量/常量）。
func sceneContextValueKey(call *ast.CallExpr, consts sceneConstLookup) (string, bool) {
	if call == nil || len(call.Args) != 1 {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel == nil || sel.Sel.Name != "Value" {
		return "", false
	}
	key, ok := sceneValueOf(call.Args[0], consts)
	return key, ok && key != ""
}

// contextKeyPresent 场景里该 context key 的"存在性"。
func (s Scene) contextKeyPresent(key string) (present, found bool) {
	k := strings.ToLower(key)
	for sk, v := range s.Values {
		if sk != k && !strings.HasSuffix(sk, k) {
			continue
		}
		switch strings.ToLower(v) {
		case "false", "0", "no", "off", "absent", "none":
			return false, true
		}
		return true, true
	}
	return false, false
}

// firstNonEmpty 第一个非空字符串（场景证据字段选择）。
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
