package action

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

// resolveBindingAt 调用位置（token.Pos）的绑定：最新实例定义 + 调用前
// 配置覆盖。初始化快照不能直接作为调用时身份依据（todo18-6）。
func resolveBindingAt(pf *parsedFunc, varName string, at token.Pos) bindingInfo {
	if varName == "" {
		return bindingInfo{}
	}
	inst, line, pkgLevel, ok := latestInstance(pf, varName, at)
	if !ok {
		return bindingInfo{id: "unknown:" + varName, callLine: pf.fset.Position(at).Line}
	}
	b := bindingFromExpr(pf, inst, line, pkgLevel, varName, at, 0)
	b.callLine = pf.fset.Position(at).Line
	b.initialLine = line
	if l := packageDeclLine(pf, varName); l > 0 {
		b.initialLine = l
	}
	b.line = line
	applyFieldModifications(pf, varName, at, &b)
	return b
}

// packageDeclLine 包级变量声明行（局部重赋值不覆盖首次声明位置）。
func packageDeclLine(pf *parsedFunc, name string) int {
	line := 0
	ast.Inspect(pf.parsed, func(n ast.Node) bool {
		if line > 0 {
			return false
		}
		gd, ok := n.(*ast.GenDecl)
		if !ok {
			return true
		}
		for _, sp := range gd.Specs {
			vs, ok := sp.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, id := range vs.Names {
				if id.Name == name {
					line = pf.fset.Position(gd.Pos()).Line
					return false
				}
			}
		}
		return true
	})
	return line
}

// latestInstance 调用前最新的实例定义（局部重赋值优先；否则包级初始化）。
func latestInstance(pf *parsedFunc, varName string, at token.Pos) (ast.Expr, int, bool, bool) {
	var best *assignBinding
	bs := declaredBindings(pf, varName)
	for i := range bs {
		b := &bs[i]
		if b.rhs == nil || b.line == 0 {
			continue
		}
		pos := b.pos
		if pos == token.NoPos {
			pos = b.stmt.Pos()
		}
		if pos > at || !blockContains(b.block, pf.fset, at) {
			continue
		}
		if best == nil || pos > best.pos || (pos == best.pos && blockSize(pf, b.block) < blockSize(pf, best.block)) {
			best = b
		}
	}
	if best != nil {
		return best.rhs, best.line, false, true
	}
	if expr, line, pkgLevel := declInit(pf, varName); expr != nil {
		return expr, line, pkgLevel, true
	}
	return nil, 0, false, false
}
func bindingFromExpr(pf *parsedFunc, expr ast.Expr, line int, pkgLevel bool, varName string, at token.Pos, depth int) bindingInfo {
	if depth > 4 {
		return bindingInfo{line: line, initialLine: line, unknownWhy: "别名链过深"}
	}
	e := unwrapAddr(expr)
	if id, isID := e.(*ast.Ident); isID {
		if id.Name == varName {
			return bindingInfo{id: "self:" + varName, line: line, initialLine: line}
		}
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

// applyFieldModifications 调用前的接收者字段覆盖 / 别名修改 / 未知调用。
func applyFieldModifications(pf *parsedFunc, varName string, at token.Pos, b *bindingInfo) {
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
		if !ok {
			return true
		}
		pos := as.Pos()
		if pos >= at {
			return true
		}
		for i, lhs := range as.Lhs {
			sel, ok := lhs.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			root, _, _ := resolveInstanceRoot(pf, sel.X, pos, 0)
			if root != varName {
				continue
			}
			line := pf.fset.Position(pos).Line
			conditional := !blockContains(blockContaining(pf.decl.Body, pf.fset, pos), pf.fset, at)
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

	ast.Inspect(pf.decl.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || call.Pos() >= at {
			return true
		}
		sel, _ := call.Fun.(*ast.SelectorExpr)
		if sel != nil && (asyncProducerCalls[toLowerASCII(sel.Sel.Name)] || asyncConsumerCalls[toLowerASCII(sel.Sel.Name)]) {
			return true
		}
		for _, arg := range call.Args {
			root, _, _ := resolveInstanceRoot(pf, arg, call.Pos(), 0)
			if root == varName {
				unknown = "调用前存在未解析的接收者/别名传递（L" + itoaSeq(pf.fset.Position(call.Pos()).Line) + "）"
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
