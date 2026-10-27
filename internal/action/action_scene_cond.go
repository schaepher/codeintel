package action

// todo22 §5：场景判定的细节——比较（字面量/常量/内建转换）、数值比较、
// 布尔短路与取反。从 action_scene.go 拆出（行数治理）。

import (
	"go/ast"
	"strings"
)

// sceneNegateFit 取反：taken ↔ excluded，unknown 保留原因。
func sceneNegateFit(fit SceneFit, negated bool) SceneFit {
	if !negated {
		return fit
	}
	switch fit.Status {
	case SceneTaken:
		fit.Status = SceneExcluded
	case SceneExcluded:
		fit.Status = SceneTaken
	}
	fit.Reason = "negated:" + fit.Reason
	return fit
}

// matchBoolOp `&&` / `||` 的短路判定。
func (s Scene) matchBoolOp(bin *ast.BinaryExpr, args, srcs map[string]string, consts sceneConstLookup) SceneFit {
	l := s.matchCondSrc(bin.X, args, srcs, consts)
	r := s.matchCondSrc(bin.Y, args, srcs, consts)
	if bin.Op.String() == "&&" {
		// 任一为假 → 整条为假（短路，可以排除分支）
		if l.Status == SceneExcluded {
			return l
		}
		if r.Status == SceneExcluded {
			return r
		}
		if l.Status == SceneTaken && r.Status == SceneTaken {
			return SceneFit{Status: SceneTaken, Key: firstNonEmpty(l.Key, r.Key),
				Field: firstNonEmpty(l.Field, r.Field), Literal: firstNonEmpty(l.Literal, r.Literal),
				Expected: firstNonEmpty(l.Expected, r.Expected), Reason: "and:" + l.Reason + " && " + r.Reason}
		}
		return SceneFit{Status: SceneUnknown, Field: firstNonEmpty(l.Field, r.Field),
			Literal: firstNonEmpty(l.Literal, r.Literal), Reason: "unknown:and-partial"}
	}
	// ||
	if l.Status == SceneTaken {
		return l
	}
	if r.Status == SceneTaken {
		// todo23 §5：左侧未知（未知调用可能改写右侧读取的变量）时不能因
		// 右侧为真直接判 taken——保留 unknown（不忽略前面的未知调用）。
		if l.Status == SceneUnknown {
			return SceneFit{Status: SceneUnknown, Field: firstNonEmpty(l.Field, r.Field),
				Literal: firstNonEmpty(l.Literal, r.Literal), Reason: "unknown:or-left-may-mutate"}
		}
		return r
	}
	if l.Status == SceneExcluded && r.Status == SceneExcluded {
		return SceneFit{Status: SceneExcluded, Key: firstNonEmpty(l.Key, r.Key),
			Field: firstNonEmpty(l.Field, r.Field), Literal: firstNonEmpty(l.Literal, r.Literal),
			Reason: "or:" + l.Reason + " || " + r.Reason}
	}
	return SceneFit{Status: SceneUnknown, Field: firstNonEmpty(l.Field, r.Field),
		Literal: firstNonEmpty(l.Literal, r.Literal), Reason: "unknown:or-partial"}
}

// matchCompareCond 单个比较（== / !=）的判定。
func (s Scene) matchCompareCond(cond ast.Expr, args, srcs map[string]string, consts sceneConstLookup) SceneFit {
	bin, ok := cond.(*ast.BinaryExpr)
	if !ok {
		return SceneFit{Status: SceneUnknown, Reason: "unknown:not-comparison"}
	}
	op := bin.Op.String()
	if op != "==" && op != "!=" {
		return SceneFit{Status: SceneUnknown, Reason: "unknown:op-" + op}
	}
	// todo22 P1：AOP bypass 的运行时事实（`ctx.Value("bypass") != nil`）——
	// 只有显式 --scene 给出该键时才判定；未给出保持 unknown（保留两分支）。
	if fit, ok := s.matchContextValueCond(bin, op, consts); ok {
		return fit
	}
	field, lit, ok := sceneCompare(bin.X, bin.Y, consts)
	if !ok {
		// 字面量/常量在左侧（"CN" == req.Region / SourceB == req.Source）
		if f2, l2, ok2 := sceneCompare(bin.Y, bin.X, consts); ok2 {
			field, lit, ok = f2, l2, true
		}
	}
	if !ok {
		return SceneFit{Status: SceneUnknown, Reason: "unknown:no-literal-pair"}
	}
	// 值来源：形参传播优先，其次场景键
	expected, key, reason := "", "", ""
	if v, ok := args[field]; ok {
		if v == sceneArgUnknown {
			// 调用点有定义但无法证明唯一值 → unknown（不回退场景键）
			return SceneFit{Status: SceneUnknown, Field: field, Literal: lit,
				Reason: "unknown:arg-value", Source: srcs[field]}
		}
		if v != "" {
			expected, key, reason = v, field, "scene-arg:"+field+"="+v
		}
	}
	if expected == "" {
		if k, v, found := s.lookup(field); found {
			expected, key, reason = v, k, "scene:"+k+"="+v
		} else {
			reason := "unknown:no-scene-key"
			if s.Empty() {
				reason = "unknown:no-scene"
			}
			return SceneFit{Status: SceneUnknown, Field: field, Literal: lit, Reason: reason}
		}
	}
	equal := sceneValueEqual(lit, expected)
	status := SceneExcluded
	if (op == "==" && equal) || (op == "!=" && !equal) {
		status = SceneTaken
	}
	source := "scene:" + key
	if reason != "" && strings.HasPrefix(reason, "scene-arg:") {
		source = srcs[field]
	}
	return SceneFit{Status: status, Key: key, Field: field, Literal: lit,
		Expected: expected, Reason: reason, Source: source}
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

// sceneCompare 从比较两侧提取"字段表达式 + 字面量/常量值"。
// todo22 §5：支持整型/浮点/字符字面量、包内常量与内建整数转换
// （int32(x) / int(x) / Source(7) 等）——不把枚举名称当值。
func sceneCompare(fieldExpr, litExpr ast.Expr, consts sceneConstLookup) (field, lit string, ok bool) {
	lit, ok = sceneValueOf(litExpr, consts)
	if !ok {
		return "", "", false
	}
	field, ok = sceneFieldOf(fieldExpr)
	return field, lit, ok
}

// sceneValueOf 表达式的"可比较值"：字面量（字符串/整数/浮点/字符）或包内
// 常量（按 const 声明解析），中间的内建转换/括号被剥离。
func sceneValueOf(e ast.Expr, consts sceneConstLookup) (string, bool) {
	// todo23 §5：内建整数转换（未遮蔽）按范围校验求值；`int32(SourceB)` 与
	// `SourceB` 得到同一个值，越界转换保持 unknown。
	for {
		call, ok := e.(*ast.CallExpr)
		if !ok {
			break
		}
		typ, isConv := sceneBuiltinConversionCall(call)
		if !isConv {
			break
		}
		inner, ok := sceneValueOf(call.Args[0], consts)
		if !ok {
			return "", false
		}
		return sceneConvertInt(typ, inner)
	}
	e = unwrapSceneExpr(e)
	switch x := e.(type) {
	case *ast.BasicLit:
		switch x.Kind.String() {
		case "STRING":
			return strings.Trim(x.Value, "\"`"), true
		case "INT", "FLOAT", "CHAR":
			return x.Value, true
		}
	case *ast.Ident:
		if v, ok := sceneConstValue(consts, x.Name); ok {
			return v, true
		}
	case *ast.SelectorExpr:
		// todo22 P1：跨包常量（otherpkg.Const）——按 import 路径按需解析
		// 目标包的 const 声明；解析不到就是"不是常量"，保持未知。
		// todo23 §5：导入别名被局部变量遮蔽时不得当包常量。
		if id, ok := x.X.(*ast.Ident); ok && id.Obj != nil && id.Obj.Kind != ast.Pkg {
			return "", false
		}
		if v, ok := sceneConstValue(consts, exprTextShallow(x)); ok {
			return v, true
		}
	}
	return "", false
}

// sceneFieldOf 表达式的"字段身份"（req.Region / region），同样剥离转换。
func sceneFieldOf(e ast.Expr) (string, bool) {
	e = unwrapSceneExpr(e)
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name, true
	case *ast.SelectorExpr:
		return exprTextShallow(x), true
	}
	return "", false
}

// unwrapSceneExpr 剥括号与单参数内建转换：int32(x) / int(x) / Source(7)。
func unwrapSceneExpr(e ast.Expr) ast.Expr {
	for {
		switch x := e.(type) {
		case *ast.ParenExpr:
			e = x.X
		case *ast.CallExpr:
			if _, ok := sceneBuiltinConversionCall(x); !ok {
				return e
			}
			e = x.Args[0]
		default:
			return e
		}
	}
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

// sceneArgValues 实参解析结果 → (值, 来源, 未知原因) 三切片。

// paramReasonArgs 形参名 → 未知原因（与 paramLiteralArgs 位置对齐）。

// paramSourceArgs 形参名 → 值来源（与 paramLiteralArgs 位置对齐）。

// paramLiteralArgs 形参名 → 实参字面量（按签名位置对齐；跳过 receiver）。

// matchesField 场景是否"关心"这个字段（键名与字段名/列名匹配即算）。
// 用于 effects 的写入裁剪：只保留场景涉及的字段（不推断值）。
func (s Scene) matchesField(field string) bool {
	_, _, ok := s.lookup(field)
	return ok
}

// appendUniqueStr 追加去重（保持入参顺序——诊断文本稳定）。

// matchContextValueCond 识别 `ctx.Value("key") == nil` / `!= nil` 形态：
// 场景键 = Value 参数的字符串值（字面量/包内常量/跨包常量），场景值
// true/1/on/present → 存在；false/0/off/absent → 不存在。
//
// 这是"运行时事实"的显式输入：没有 --scene 键时不得猜，保持 unknown。

// sceneNilCompareOperands 比较两侧恰好一侧是 nil，返回另一侧的调用表达式。

// sceneContextValueKey `x.Value(<key>)` → key 的字符串值（字面量/常量）。

// contextKeyPresent 场景里该 context key 的"存在性"。
