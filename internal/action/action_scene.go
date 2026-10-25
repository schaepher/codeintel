package action

// todo10-5：场景条件（`--scene k=v`）——静态可判定的键值约束。
//
// 作用域与判据（用户确认的 Q1/Q2）：
//   - 只做**静态可判定**匹配：条件里的字面量与场景值比对（不做值流传播的
//     完整求解）
//   - 一层传播：调用点实参是字面量、且被调函数的形参参与 `if` 条件时，
//     该条件按实参字面量判定（不跨多层；跨多层记 unverified:condition-depth）
//   - 未匹配/未知一律保留候选，只把判断结果作为依据（verdict/stop 语义与
//     todo10-3/8 一致）——静态推断不是线上事实

import (
	"go/ast"
	"strings"
)

// Scene 场景条件（`--scene region=CN,platform=ios`）。键名做大小写不敏感匹配。
type Scene struct {
	Values map[string]string
}

// 场景对单个分支/条件的判定结果。
const (
	SceneTaken    = "taken"    // 场景命中"条件为真"的分支
	SceneExcluded = "excluded" // 场景命中"条件为假"的分支（分支内容被剔除）
	SceneUnknown  = "unknown"  // 无法静态判定（保留候选）
)

// SceneFit 一次场景判定的依据（挂在节点上，渲染层据此标注）。
type SceneFit struct {
	Status   string `json:"status"`             // taken|excluded|unknown
	Key      string `json:"key,omitempty"`      // 命中的场景键（region）
	Field    string `json:"field,omitempty"`    // 条件里的字段/形参（req.Region）
	Literal  string `json:"literal,omitempty"`  // 条件里的字面量（"CN"）
	Expected string `json:"expected,omitempty"` // 场景给出的值（CN）
	Reason   string `json:"reason,omitempty"`   // scene:<k=v> / scene-arg:<param>=<lit> / unknown:*
}

// NewScene 解析 `--scene` 参数：每项为 `k=v`（也接受逗号分隔的多个）。
func NewScene(pairs []string) Scene {
	sc := Scene{Values: map[string]string{}}
	for _, p := range pairs {
		for _, part := range strings.Split(p, ",") {
			k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
			if !ok {
				continue
			}
			k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
			if k != "" && v != "" {
				sc.Values[k] = v
			}
		}
	}
	return sc
}

// Empty 是否未指定场景。
func (s Scene) Empty() bool { return len(s.Values) == 0 }

// sceneSummary meta 汇总（取用/剔除/未知各多少分支）。
type sceneSummary struct {
	Applied  bool `json:"applied"`
	Taken    int  `json:"taken"`
	Excluded int  `json:"excluded"`
	Unknown  int  `json:"unknown"`
}

// matchCond 判定条件表达式与场景的关系。args 为"形参名 → 实参字面量"
// （一层传播；nil 表示没传）。只识别 `field == "lit"` / `field != "lit"`
// 与 `"lit" == field` 形态（必要时剥离一层括号/取反 `!`）。
func (s Scene) matchCond(cond ast.Expr, args map[string]string) SceneFit {
	negated := false
	for {
		switch e := cond.(type) {
		case *ast.ParenExpr:
			cond = e.X
			continue
		case *ast.UnaryExpr:
			if e.Op.String() == "!" {
				negated = !negated
				cond = e.X
				continue
			}
		}
		break
	}
	bin, ok := cond.(*ast.BinaryExpr)
	if !ok {
		return SceneFit{Status: SceneUnknown, Reason: "unknown:not-comparison"}
	}
	op := bin.Op.String()
	if op != "==" && op != "!=" {
		return SceneFit{Status: SceneUnknown, Reason: "unknown:op-" + op}
	}
	field, lit, ok := sceneCompare(bin.X, bin.Y)
	if !ok {
		// 字面量在左侧（"CN" == req.Region）
		if f2, l2, ok2 := sceneCompare(bin.Y, bin.X); ok2 {
			field, lit, ok = f2, l2, true
		}
	}
	if !ok {
		return SceneFit{Status: SceneUnknown, Reason: "unknown:no-literal-pair"}
	}
	// 值来源：形参传播优先，其次场景键
	expected, key, reason := "", "", ""
	if v, ok := args[field]; ok && v != "" {
		expected, key, reason = v, field, "scene-arg:"+field+"="+v
	} else if k, v, found := s.lookup(field); found {
		expected, key, reason = v, k, "scene:"+k+"="+v
	} else {
		reason := "unknown:no-scene-key"
		if s.Empty() {
			reason = "unknown:no-scene"
		}
		return SceneFit{Status: SceneUnknown, Field: field, Literal: lit, Reason: reason}
	}
	equal := lit == expected
	if negated {
		equal = !equal
	}
	status := SceneExcluded
	if (op == "==" && equal) || (op == "!=" && !equal) {
		status = SceneTaken
	}
	return SceneFit{Status: status, Key: key, Field: field, Literal: lit,
		Expected: expected, Reason: reason}
}

// lookup 场景键匹配字段名：大小写不敏感；字段末段相等或以 key 结尾
// （req.Region / o.Region / region 都命中 region）。
func (s Scene) lookup(field string) (key, value string, ok bool) {
	f := strings.ToLower(field)
	last := f
	if i := strings.LastIndex(f, "."); i >= 0 {
		last = f[i+1:]
	}
	for k, v := range s.Values {
		if last == k || strings.HasSuffix(last, k) {
			return k, v, true
		}
	}
	return "", "", false
}

// sceneCompare 从比较两侧提取"字段表达式 + 字符串字面量"。
func sceneCompare(fieldExpr, litExpr ast.Expr) (field, lit string, ok bool) {
	l, isLit := litExpr.(*ast.BasicLit)
	if !isLit || l.Kind.String() != "STRING" {
		return "", "", false
	}
	switch e := fieldExpr.(type) {
	case *ast.Ident:
		field = e.Name
	case *ast.SelectorExpr:
		field = exprTextShallow(e)
	default:
		return "", "", false
	}
	return field, strings.Trim(l.Value, "\"`"), true
}

// exprTextShallow 选择器表达式文本（pkg.Field / recv.Field——只到两层，
// 足够匹配场景键）。
func exprTextShallow(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return exprTextShallow(x.X) + "." + x.Sel.Name
	}
	return ""
}

// callArgLiterals 调用点实参里的字符串字面量（按位置；非字面量记空串）。
// 用于一层条件传播（形参 → 实参字面量）。
func callArgLiterals(call *ast.CallExpr) []string {
	if call == nil {
		return nil
	}
	out := make([]string, 0, len(call.Args))
	for _, a := range call.Args {
		if lit, ok := a.(*ast.BasicLit); ok && lit.Kind.String() == "STRING" {
			out = append(out, strings.Trim(lit.Value, "\"`"))
			continue
		}
		out = append(out, "")
	}
	return out
}

// paramLiteralArgs 形参名 → 实参字面量（按签名位置对齐；跳过 receiver）。
func paramLiteralArgs(fl *ast.FieldList, lits []string) map[string]string {
	if fl == nil || len(lits) == 0 {
		return nil
	}
	out := map[string]string{}
	idx := 0
	for _, f := range fl.List {
		n := len(f.Names)
		if n == 0 {
			n = 1
		}
		for i := 0; i < n; i++ {
			if idx < len(lits) && lits[idx] != "" {
				name := ""
				if i < len(f.Names) {
					name = f.Names[i].Name
				}
				if name != "" {
					out[name] = lits[idx]
				}
			}
			idx++
		}
	}
	return out
}

// matchesField 场景是否"关心"这个字段（键名与字段名/列名匹配即算）。
// 用于 effects 的写入裁剪：只保留场景涉及的字段（不推断值）。
func (s Scene) matchesField(field string) bool {
	_, _, ok := s.lookup(field)
	return ok
}
