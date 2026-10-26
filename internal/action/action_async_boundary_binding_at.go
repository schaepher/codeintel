package action

// todo18/19-6：按调用位置解析实例与绑定（顺序/分支/别名实例身份）。

import (
	"go/ast"
	"go/token"
	"strings"
)

func bindingText(b bindingInfo) string {
	base := b.sig
	if base == "" || base == "{}" {
		base = "默认/未知"
	}
	txt := base + "（初始化 L" + itoaSeq(b.initialLine) + "）"
	if b.line > 0 && b.line != b.initialLine {
		txt += "；重赋值 L" + itoaSeq(b.line)
	}
	if b.modLine > 0 {
		txt += "；调用前覆盖 L" + itoaSeq(b.modLine)
	}
	if b.callLine > 0 {
		txt += "；调用 L" + itoaSeq(b.callLine)
	}
	if b.unknownWhy != "" {
		txt += "；" + b.unknownWhy
	}
	return txt
}

// instanceRef 调用前的实例身份（id 用于区分旧别名捕获 vs 重赋值新实例）。
type instanceRef struct {
	id       string
	expr     ast.Expr
	line     int
	pkgLevel bool
	known    bool
	condLine int // 调用前条件分支中的重赋值（值未知）
	why      string
}

// instanceAt 调用位置 at 的当前实例（顺序：无条件最新 > 条件候选 > 包级）。
func instanceAt(pf *parsedFunc, name string, at token.Pos) instanceRef {
	var best *assignBinding
	condLine := 0
	for i := range declaredBindings(pf, name) {
		b := &declaredBindings(pf, name)[i]
		if b.rhs == nil || b.line == 0 {
			continue
		}
		pos := b.pos
		if pos == token.NoPos && b.stmt != nil {
			pos = b.stmt.Pos()
		}
		if pos == token.NoPos || pos > at {
			continue
		}
		if !blockContains(b.block, pf.fset, at) {
			if b.line > condLine {
				condLine = b.line
			}
			continue
		}
		if best == nil || pos > b.pos || (pos == b.pos && blockSize(pf, b.block) < blockSize(pf, best.block)) {
			best = b
		}
	}
	if best != nil && best.line >= condLine {
		return instanceRefFromBinding(pf, name, best, at)
	}
	if condLine > 0 {
		return instanceRef{known: false,
			why: "调用前存在条件分支内重赋值（L" + itoaSeq(condLine) + "），调用时值未知",
			condLine: condLine}
	}
	if expr, line, ok := packageDeclExpr(pf, name); ok {
		return instanceRef{id: "pkg:" + pf.pkg + ":" + name + ":" + itoaSeq(line),
			expr: expr, line: line, pkgLevel: true, known: true}
	}
	return instanceRef{}
}

// instanceRefFromBinding 局部赋值 → 实例身份（别名递归到捕获的实例）。
func instanceRefFromBinding(pf *parsedFunc, name string, b *assignBinding, at token.Pos) instanceRef {
	e := unwrapAddr(b.rhs)
	if id, ok := e.(*ast.Ident); ok {
		inner := instanceAt(pf, id.Name, b.pos)
		if inner.known {
			return inner
		}
	}
	return instanceRef{id: "local:" + pf.id + ":" + name + ":" + itoaSeq(b.line),
		expr: b.rhs, line: b.line, known: true}
}

// instanceIDAt 表达式在 pos 时指向的实例 id（别名/&/直接变量）。
func instanceIDAt(pf *parsedFunc, e ast.Expr, pos token.Pos) string {
	e = unwrapAddr(e)
	id, ok := e.(*ast.Ident)
	if !ok {
		return ""
	}
	r := instanceAt(pf, id.Name, pos)
	if !r.known {
		return ""
	}
	return r.id
}

// packageDeclExpr 包级变量初始化表达式（不看函数内重赋值）。
func packageDeclExpr(pf *parsedFunc, name string) (ast.Expr, int, bool) {
	for _, d := range pf.parsed.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, sp := range gd.Specs {
			vs, ok := sp.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, id := range vs.Names {
				if id.Name == name && len(vs.Values) == len(vs.Names) {
					return vs.Values[i], pf.fset.Position(gd.Pos()).Line, true
				}
			}
		}
	}
	return nil, 0, false
}

// packageDeclLine 包级变量声明行（局部重赋值不覆盖首次声明位置）。
func packageDeclLine(pf *parsedFunc, name string) int {
	_, line, _ := packageDeclExpr(pf, name)
	return line
}

// resolveBindingAt 调用位置的绑定：当前实例 + 调用前配置覆盖。
func resolveBindingAt(pf *parsedFunc, varName string, at token.Pos) bindingInfo {
	if varName == "" {
		return bindingInfo{}
	}
	inst := instanceAt(pf, varName, at)
	callLine := pf.fset.Position(at).Line
	if !inst.known {
		return bindingInfo{id: "unknown:" + varName, callLine: callLine,
			unknownWhy: inst.why}
	}
	b := bindingFromExpr(pf, inst.expr, inst.line, inst.pkgLevel, varName, at, 0)
	b.callLine = callLine
	b.initialLine = inst.line
	if l := packageDeclLine(pf, varName); l > 0 {
		b.initialLine = l
	}
	b.line = inst.line
	if b.id == "" {
		b.id = inst.id
	}
	applyFieldModifications(pf, varName, inst.id, at, &b)
	return b
}

func bindingFromExpr(pf *parsedFunc, expr ast.Expr, line int, pkgLevel bool, varName string, at token.Pos, depth int) bindingInfo {
	if depth > 4 {
		return bindingInfo{line: line, initialLine: line, unknownWhy: "别名链过深"}
	}
	e := unwrapAddr(expr)
	if id, isID := e.(*ast.Ident); isID {
		inner := resolveBindingAt(pf, id.Name, at)
		if inner.initialLine == 0 {
			inner.initialLine = line
		}
		return inner
	}
	sig, explicit, ok := compositeConfig(expr)
	id := "local:" + pf.id + ":" + varName + ":" + itoaSeq(line)
	if pkgLevel {
		id = "pkg:" + pf.pkg + ":" + varName + ":" + itoaSeq(line)
	}
	b := bindingInfo{id: id, sig: sig, line: line, initialLine: line,
		explicit: explicit, known: ok && (explicit || line > 0)}
	if !ok {
		b.unknownWhy = "实例初始化不是可解析的配置字面量"
	}
	return b
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
