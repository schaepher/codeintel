package action

// 前端源码解析辅助（从 action_seq_frontend.go 拆出）。

import (
	"strings"
)

// seqFrontendStringLit 字符串字面量 → 内容。
func seqFrontendStringLit(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if len(s) < 2 {
		return "", false
	}
	if (s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"') ||
		(s[0] == '`' && s[len(s)-1] == '`') {
		return s[1 : len(s)-1], true
	}
	return "", false
}

// seqFrontendSplitArgs 实参文本 → 简单实参列表（不处理嵌套括号）。
func seqFrontendSplitArgs(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []string
	depth := 0
	cur := strings.Builder{}
	for _, r := range raw {
		switch r {
		case '{', '[', '(':
			depth++
		case '}', ']', ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(cur.String()))
				cur.Reset()
				continue
			}
		}
		cur.WriteRune(r)
	}
	out = append(out, strings.TrimSpace(cur.String()))
	return out
}

// seqFrontendEvalArg 实参文本 → 已知值（字面量 / 调用方形参绑定）。
func seqFrontendEvalArg(raw string, bindings map[string]string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if lit, ok := seqFrontendStringLit(raw); ok {
		return lit, true
	}
	if v, ok := bindings[raw]; ok {
		return v, true
	}
	return "", false
}

// seqFrontendParams `id: string, x` → 参数名列表。
func seqFrontendParams(raw string) []string {
	var out []string
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if i := strings.IndexAny(p, ":="); i >= 0 {
			p = strings.TrimSpace(p[:i])
		}
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// seqFrontendBody 从函数签名末尾开始按花括号配平截取函数体。
func seqFrontendBody(src string, from int) string {
	start := strings.Index(src[from:], "{")
	if start < 0 {
		return ""
	}
	start += from
	depth := 0
	for i := start; i < len(src); i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[start : i+1]
			}
		}
	}
	return src[start:]
}

// seqFrontendResolveFetch 沿包装函数链解析 fetch（实参位置传播；深度 ≤3）。
func seqFrontendResolve(fn seqFrontendFunc, funcs map[string]seqFrontendFunc,
	imports map[string]map[string]seqFrontendImport, bindings map[string]string,
	depth int) (string, string, map[string]seqFrontendBodyOrigin, bool) {
	if depth > 3 {
		return "", "", nil, false
	}
	// 有 fetch 的函数：解析最终路径/方法与请求体（相对本函数形参）。
	if fn.fetchArg != "" || fn.path != "" {
		path := fn.path
		if fn.fetchArg != "" {
			p, ok := seqFrontendEvalArg(fn.fetchArg, bindings)
			if !ok {
				return "", "", nil, false
			}
			path = p
		}
		m := fn.method
		if m == "" {
			m = "GET"
		}
		return path, m, seqFrontendBodyOrigins(fn), true
	}
	for _, c := range fn.calls {
		next, ok := seqFrontendLookup(c.name, fn.file, funcs, imports)
		if !ok {
			continue
		}
		nb := map[string]string{}
		argOrigins := map[string]seqFrontendBodyOrigin{}
		for i, arg := range c.args {
			if i >= len(next.params) {
				break
			}
			if v, ok := seqFrontendEvalArg(arg, bindings); ok {
				nb[next.params[i]] = v
			}
			argOrigins[next.params[i]] = seqFrontendArgOrigin(arg, fn.params)
		}
		if p, m, body, ok2 := seqFrontendResolve(next, funcs, imports, nb, depth+1); ok2 {
			return p, m, seqFrontendRebaseBody(body, next.params, argOrigins), true
		}
	}
	return "", "", nil, false
}

// seqFrontendResolveFetch 兼容入口：只取路径/方法。
func seqFrontendResolveFetch(fn seqFrontendFunc, funcs map[string]seqFrontendFunc,
	imports map[string]map[string]seqFrontendImport, bindings map[string]string,
	depth int) (string, string, bool) {
	p, m, _, ok := seqFrontendResolve(fn, funcs, imports, bindings, depth)
	return p, m, ok
}

// seqFrontendArgOrigin 调用实参相对调用者形参的来源。
func seqFrontendArgOrigin(arg string, params []string) seqFrontendBodyOrigin {
	arg = strings.TrimSpace(arg)
	if lit, ok := seqFrontendStringLit(arg); ok {
		return seqFrontendBodyOrigin{kind: "literal", literal: lit}
	}
	for i, p := range params {
		if p == arg {
			return seqFrontendBodyOrigin{kind: "parameter", parameter: i}
		}
	}
	return seqFrontendBodyOrigin{kind: "unknown"}
}

// seqFrontendRebaseBody 把被调函数体内的形参来源替换为调用点来源。
func seqFrontendRebaseBody(body map[string]seqFrontendBodyOrigin, calleeParams []string,
	argOrigins map[string]seqFrontendBodyOrigin) map[string]seqFrontendBodyOrigin {
	if len(body) == 0 {
		return nil
	}
	out := map[string]seqFrontendBodyOrigin{}
	for name, o := range body {
		if o.kind == "parameter" && o.parameter >= 0 && o.parameter < len(calleeParams) {
			if rebased, ok := argOrigins[calleeParams[o.parameter]]; ok {
				o = rebased
			} else {
				o = seqFrontendBodyOrigin{kind: "unknown"}
			}
		}
		out[name] = o
	}
	return out
}

// seqFrontendBodyOrigins 函数自身请求体的字段来源（相对本函数形参）。
func seqFrontendBodyOrigins(fn seqFrontendFunc) map[string]seqFrontendBodyOrigin {
	if len(fn.body) == 0 {
		return nil
	}
	out := map[string]seqFrontendBodyOrigin{}
	for _, f := range fn.body {
		expr := strings.TrimSpace(f.expr)
		if lit, ok := seqFrontendStringLit(expr); ok {
			out[f.name] = seqFrontendBodyOrigin{kind: "literal", literal: lit}
			continue
		}
		idx := -1
		for i, p := range fn.params {
			if p == expr {
				idx = i
				break
			}
		}
		if idx >= 0 {
			out[f.name] = seqFrontendBodyOrigin{kind: "parameter", parameter: idx}
			continue
		}
		out[f.name] = seqFrontendBodyOrigin{kind: "unknown"}
	}
	return out
}

// seqFrontendBodyJSON 内部来源 → JSON 契约。
func seqFrontendBodyJSON(body map[string]seqFrontendBodyOrigin) map[string]SeqFrontendBodyValue {
	if len(body) == 0 {
		return nil
	}
	out := map[string]SeqFrontendBodyValue{}
	for name, o := range body {
		v := SeqFrontendBodyValue{Kind: o.kind, Literal: o.literal}
		if o.kind == "parameter" {
			idx := o.parameter
			v.Parameter = &idx
		}
		out[name] = v
	}
	return out
}

// seqFrontendParseBody 从 fetch options 提取 body 对象字段。
func seqFrontendParseBody(body string) []seqFrontendBodyField {
	m := seqFrontendBodyRe.FindStringSubmatch(body)
	if m == nil {
		return nil
	}
	var out []seqFrontendBodyField
	for _, raw := range strings.Split(m[1], ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		name, expr := raw, raw
		if i := strings.Index(raw, ":"); i >= 0 {
			name = strings.TrimSpace(raw[:i])
			expr = strings.TrimSpace(raw[i+1:])
		}
		name = strings.Trim(name, `"'`)
		if name == "" {
			continue
		}
		out = append(out, seqFrontendBodyField{name: name, expr: expr})
	}
	return out
}

// seqFrontendImportSpec `click as submitClick` → (submitClick, click)。
func seqFrontendImportSpec(spec string) (local, exported string) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", ""
	}
	parts := strings.Fields(spec)
	if len(parts) >= 3 && parts[1] == "as" {
		return parts[2], parts[0]
	}
	return parts[0], parts[0]
}
