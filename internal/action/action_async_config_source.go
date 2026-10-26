package action

// P1-01：运行时配置 / 动态别名的来源定位。
//
// 静态证明的边界：如果消息/存储实例的地址、主题或客户端来自**运行时值**
// （配置文件字段、环境变量、工厂函数、反射、类型断言），"是不是同一个
// 系统/配置"在编译期无法闭合。此前这类形态统一落到一句
// "实例初始化不是可解析的配置字面量"——用户不知道究竟是哪个值、在哪一行
// 决定了它，也无法判断该补哪类证据。
//
// 本文件只做两件**不改变判定等级**的事：
//  1. 把配置值按来源分类并定位（literal / 变量 / 运行时配置字段 / 环境变量 /
//     reflect / 类型断言 / 普通调用），写进 binding 的 unknownWhy；
//  2. 识别"动态别名"形态（reflect.Value 取值、接口类型断言），给出具体
//     原因与行号，而不是笼统的"未建模"。
// corroborated 仍只能来自：同一实例声明、或同类型 + 同非空字面量配置；
// 运行时来源一律 unverified（todo18 的纪律：前提缺失就降级并指出缺口）。

import (
	"go/ast"
	"go/token"
	"strings"
)

// configValueKind 配置值来源类别。
type configValueKind string

const (
	cfgKindLiteral  configValueKind = "literal"  // 字面量（可静态闭合）
	cfgKindVariable configValueKind = "variable" // 局部/包级变量（值未知）
	cfgKindConfig   configValueKind = "config"   // 运行时配置字段（cfg.Addr）
	cfgKindEnv      configValueKind = "env"      // os.Getenv(...)
	cfgKindReflect  configValueKind = "reflect"  // reflect.* 取值
	cfgKindAssert   configValueKind = "assert"   // 接口类型断言 v.(*T)
	cfgKindCall     configValueKind = "call"     // 其他运行时调用
	cfgKindExpr     configValueKind = "expr"     // 复合表达式（拼接/格式化等）
)

// configValue 一个配置值的分类结果（text 为用户可读的来源文本）。
type configValue struct {
	text string
	kind configValueKind
	line int
}

// exprSourceText 表达式源码文本（失败回退类型名），用于把来源写给用户。
func exprSourceText(pf *parsedFunc, e ast.Expr) string {
	if pf == nil || e == nil || pf.fset == nil {
		return ""
	}
	if t := strings.TrimSpace(exprText(pf.fset, e)); t != "" {
		return t
	}
	return ""
}

// classifyConfigValue 配置值表达式 → 来源类别 + 位置（P1-01）。
func classifyConfigValue(pf *parsedFunc, e ast.Expr) configValue {
	if e == nil {
		return configValue{kind: cfgKindVariable, text: "未知"}
	}
	line := 0
	if pf != nil && pf.fset != nil {
		line = pf.fset.Position(e.Pos()).Line
	}
	text := exprSourceText(pf, e)
	switch x := e.(type) {
	case *ast.BasicLit:
		return configValue{text: x.Value, kind: cfgKindLiteral, line: line}
	case *ast.Ident:
		if x.Name == "true" || x.Name == "false" || x.Name == "nil" {
			return configValue{text: x.Name, kind: cfgKindLiteral, line: line}
		}
		return configValue{text: x.Name, kind: cfgKindVariable, line: line}
	case *ast.CallExpr:
		if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
			if id, ok2 := sel.X.(*ast.Ident); ok2 {
				switch {
				case id.Name == "reflect":
					return configValue{text: text, kind: cfgKindReflect, line: line}
				case id.Name == "os" && sel.Sel.Name == "Getenv":
					return configValue{text: text, kind: cfgKindEnv, line: line}
				}
			}
		}
		return configValue{text: text, kind: cfgKindCall, line: line}
	case *ast.SelectorExpr:
		// cfg.KafkaAddr：运行时配置字段（值在运行期来自配置源）
		return configValue{text: text, kind: cfgKindConfig, line: line}
	case *ast.TypeAssertExpr:
		return configValue{text: text, kind: cfgKindAssert, line: line}
	case *ast.BinaryExpr:
		// 字符串拼接/格式化：参与项里可能有反射或环境变量，向下看一层
		if _, _, ok := dynamicAliasReason(pf, e); ok {
			kind := cfgKindExpr
			if hasReflectOrAssert(x) {
				kind = cfgKindReflect
			}
			return configValue{text: text, kind: kind, line: line}
		}
		return configValue{text: text, kind: cfgKindExpr, line: line}
	}
	// 其他形态（索引、切片、复合字面量…）：结构化但不含字面量值
	if hasReflectOrAssert(e) {
		return configValue{text: text, kind: cfgKindReflect, line: line}
	}
	return configValue{text: text, kind: cfgKindExpr, line: line}
}

// sourceReason 人类可读的来源说明（含行号）。
func sourceReason(v configValue) string {
	at := ""
	if v.line > 0 {
		at = "（L" + itoaSeq(v.line) + "）"
	}
	switch v.kind {
	case cfgKindConfig:
		return "运行时配置字段 " + v.text + at
	case cfgKindEnv:
		return "环境变量 " + v.text + at
	case cfgKindReflect:
		return "反射取值 " + v.text + at
	case cfgKindAssert:
		return "接口类型断言 " + v.text + at
	case cfgKindCall:
		return "运行时调用 " + v.text + at
	case cfgKindVariable:
		return "变量 " + v.text + at
	case cfgKindExpr:
		return "运行时表达式 " + v.text + at
	}
	return v.text + at
}

// configSignature 把多个配置字段值拼成稳定签名（字面量原样；运行时值前缀 ~）。
// 调用方按 key 排序后传入。
func configSignature(parts []string) string {
	return strings.Join(parts, ",")
}

// tokenLine 表达式起始行（0 = 未知）。
func tokenLine(pf *parsedFunc, pos token.Pos) int {
	if pf == nil || pf.fset == nil || pos == token.NoPos {
		return 0
	}
	return pf.fset.Position(pos).Line
}

// runtimeInitReason 非字面量初始化表达式的原因说明（含来源与行号）。
func runtimeInitReason(pf *parsedFunc, expr ast.Expr) string {
	if expr == nil {
		return "实例初始化缺失（无表达式）"
	}
	if kind, line, ok := dynamicAliasReason(pf, expr); ok {
		return dynamicAliasText(kind, line)
	}
	text := exprSourceText(pf, expr)
	line := tokenLine(pf, expr.Pos())
	loc := ""
	if line > 0 {
		loc = "（L" + itoaSeq(line) + "）"
	}
	// 工厂/构造函数：把实参里的运行时来源一并指出
	if call, ok := unwrapAddr(expr).(*ast.CallExpr); ok {
		var srcs []string
		for _, arg := range call.Args {
			v := classifyConfigValue(pf, arg)
			if v.kind == cfgKindLiteral {
				continue
			}
			srcs = append(srcs, sourceReason(v))
		}
		if len(srcs) > 0 {
			return "实例来自运行时调用 " + text + loc + "，实参：" + strings.Join(srcs, "；")
		}
		return "实例来自运行时调用 " + text + loc
	}
	if text == "" {
		return "实例初始化不是可解析的配置字面量"
	}
	return "实例初始化不是可解析的配置字面量（" + text + loc + "）"
}

// applyFieldModifications 只应用**当前实例 id** 上的覆盖（旧别名修改不算）。
func applyFieldModifications(pf *parsedFunc, varName, currentID string, at token.Pos, b *bindingInfo) {
	fields := map[string]string{}
	explicit := b.explicit
	if b.sig != "" && b.sig != "{}" {
		for _, part := range strings.Split(b.sig, ",") {
			if k, v, ok := strings.Cut(part, "="); ok {
				fields[k] = v
			}
		}
	}
	unknown := ""
	ast.Inspect(pf.decl.Body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || as.Pos() >= at {
			return true
		}
		for i, lhs := range as.Lhs {
			sel, ok := lhs.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			if instanceIDAt(pf, sel.X, as.Pos()) != currentID {
				continue // 别名指向旧实例/其他实例：不影响当前绑定
			}
			line := pf.fset.Position(as.Pos()).Line
			conditional := !blockContains(blockContaining(pf.decl.Body, pf.fset, as.Pos()), pf.fset, at)
			var val *ast.BasicLit
			if len(as.Rhs) == len(as.Lhs) {
				val, _ = as.Rhs[i].(*ast.BasicLit)
			}
			if conditional {
				unknown = "配置字段 " + sel.Sel.Name + " 在条件分支中修改（L" + itoaSeq(line) + "），调用时值未知"
				continue
			}
			if val == nil {
				unknown = "配置字段 " + sel.Sel.Name + " 在调用前被非常量修改（L" + itoaSeq(line) + "）"
				continue
			}
			fields[sel.Sel.Name] = val.Value
			explicit = true
			b.modLine = line
		}
		return true
	})
	// 调用前把当前实例传给未解析函数，或对其调用方法（Configure）→ 未知
	ast.Inspect(pf.decl.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || call.Pos() >= at {
			return true
		}
		sel, _ := call.Fun.(*ast.SelectorExpr)
		if sel != nil && (asyncProducerCalls[toLowerASCII(sel.Sel.Name)] || asyncConsumerCalls[toLowerASCII(sel.Sel.Name)]) {
			return true
		}
		if sel != nil && instanceIDAt(pf, sel.X, call.Pos()) == currentID {
			unknown = "调用前对接收者调用方法 " + sel.Sel.Name + "（L" +
				itoaSeq(pf.fset.Position(call.Pos()).Line) + "），配置可能被修改"
			return true
		}
		for _, arg := range call.Args {
			if instanceIDAt(pf, arg, call.Pos()) == currentID {
				unknown = "调用前存在未解析的接收者/别名传递（L" +
					itoaSeq(pf.fset.Position(call.Pos()).Line) + "）"
				break
			}
		}
		return true
	})
	if len(fields) > 0 {
		keys := make([]string, 0, len(fields))
		for k := range fields {
			keys = append(keys, k)
		}
		sortStrings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, k+"="+fields[k])
		}
		b.sig = strings.Join(parts, ",")
		b.explicit = explicit
	}
	if unknown != "" {
		b.known = false
		b.unknownWhy = unknown
	}
}
