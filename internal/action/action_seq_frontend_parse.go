package action

// 前端源码解析辅助（从 action_seq_frontend.go 拆出）。

import "strings"

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

// seqFrontendBody 从函数签名末尾开始按花括号配平截取函数体。
//
// B09e.4：必须先跨过参数列表（`send({id}: Input)` 的第一个 `{` 是解构，
// 不是函数体），不能按"第一个大括号"切。
func seqFrontendBody(src string, from int) string {
	start := -1
	if open := strings.Index(src[from:], "("); open >= 0 {
		open += from
		depth := 0
		for i := open; i < len(src); i++ {
			switch src[i] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					if b := strings.Index(src[i+1:], "{"); b >= 0 {
						start = i + 1 + b
					}
					i = len(src)
				}
			}
		}
	}
	if start < 0 {
		if b := strings.Index(src[from:], "{"); b >= 0 {
			start = from + b
		}
	}
	if start < 0 {
		return ""
	}
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

// seqFrontendResolveMaxDepth 声明来源解析的共享预算（B09e.3：不是示例
// 层数；超限必须明确报告，不返回"看起来成功"的结果）。
const seqFrontendResolveMaxDepth = 16

// seqFrontendResolve 沿包装函数链解析 fetch（实参位置传播 + 有界预算）。
// reason 非空表示失败原因（dynamic_url_or_missing_fetch /
// declaration_not_found / declaration_budget_exhausted / declaration_cycle）。
func seqFrontendResolve(fn seqFrontendFunc, funcs map[string]seqFrontendFunc,
	imports map[string]map[string]seqFrontendImport, bindings map[string]string,
	depth int, visited map[string]bool) (string, string, map[string]seqFrontendBodyOrigin, string) {
	if depth > seqFrontendResolveMaxDepth {
		return "", "", nil, "declaration_budget_exhausted"
	}
	key := fn.file + "\x00" + fn.name
	if visited[key] {
		return "", "", nil, "declaration_cycle"
	}
	visited[key] = true
	// 有 fetch 的函数：解析最终路径/方法与请求体（相对本函数形参）。
	if fn.fetchArg != "" || fn.path != "" {
		path := fn.path
		if fn.fetchArg != "" {
			p, ok := seqFrontendEvalArg(fn.fetchArg, bindings)
			if !ok {
				return "", "", nil, "dynamic_url_or_missing_fetch"
			}
			path = p
		}
		m := fn.method
		if m == "" {
			m = "GET"
		}
		return path, m, seqFrontendBodyOrigins(fn), ""
	}
	firstReason := ""
	for _, c := range fn.calls {
		next, ok := seqFrontendLookup(c.name, fn.file, funcs, imports)
		if !ok {
			if firstReason == "" {
				firstReason = "declaration_not_found"
			}
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
		if p, m, body, reason := seqFrontendResolve(next, funcs, imports, nb, depth+1,
			visited); reason == "" {
			return p, m, seqFrontendRebaseBody(body, next.params, argOrigins, fn, c.pos), ""
		} else if firstReason == "" {
			firstReason = reason
		}
	}
	if firstReason == "" {
		firstReason = "dynamic_url_or_missing_fetch"
	}
	return "", "", nil, firstReason
}

// seqFrontendResolveFetch 兼容入口：只取路径/方法。
func seqFrontendResolveFetch(fn seqFrontendFunc, funcs map[string]seqFrontendFunc,
	imports map[string]map[string]seqFrontendImport, bindings map[string]string,
	depth int) (string, string, bool) {
	p, m, _, reason := seqFrontendResolve(fn, funcs, imports, bindings, depth, map[string]bool{})
	return p, m, reason == ""
}

// seqFrontendArgOrigin 调用实参相对调用者形参的来源。
func seqFrontendArgOrigin(arg string, params []string) seqFrontendBodyOrigin {
	arg = strings.TrimSpace(arg)
	if lit, ok := seqFrontendStringLit(arg); ok {
		return seqFrontendBodyOrigin{kind: "literal", literal: lit}
	}
	if arg == "" || strings.ContainsAny(arg, "[]()") {
		return seqFrontendBodyOrigin{kind: "unknown"}
	}
	parts := strings.Split(arg, ".")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
		if parts[i] == "" {
			return seqFrontendBodyOrigin{kind: "unknown"}
		}
	}
	for i, p := range params {
		if p == parts[0] {
			path := append([]string(nil), parts[1:]...)
			return seqFrontendBodyOrigin{kind: "parameter", parameter: i, path: path}
		}
	}
	return seqFrontendBodyOrigin{kind: "unknown"}
}

// seqFrontendBodyJSON 内部来源 → JSON 契约。
func seqFrontendBodyJSON(body map[string]seqFrontendBodyOrigin) map[string]SeqFrontendBodyValue {
	if len(body) == 0 {
		return nil
	}
	out := map[string]SeqFrontendBodyValue{}
	for name, o := range body {
		v := SeqFrontendBodyValue{Kind: o.kind, Literal: o.literal,
			Path: append([]string(nil), o.path...)}
		if o.kind == "parameter" || o.kind == "field_read" {
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
