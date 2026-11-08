package action

// B09e.4：前端请求体字段来源（参数/解构字段/字段路径与覆盖失效）。

import (
	"regexp"
	"strings"
)

// seqFrontendBodyOrigins 函数自身请求体的字段来源（相对本函数形参）。
func seqFrontendBodyOrigins(fn seqFrontendFunc) map[string]seqFrontendBodyOrigin {
	if len(fn.body) == 0 {
		return nil
	}
	out := map[string]seqFrontendBodyOrigin{}
	for _, f := range fn.body {
		out[f.name] = seqFrontendBodyOriginOf(fn, f.expr, fn.fetchPos)
	}
	return out
}

// seqFrontendBodyOriginOf 单个请求体字段表达式 → 形参来源（B09e.4）。
//
// `req.id` / 解构 `{id}` 保留 parameter + path；另一对象、同名局部变量、
// 字段被覆盖、动态下标一律 unknown（不借用参数身份）。
func seqFrontendBodyOriginOf(fn seqFrontendFunc, expr string, cutoff int) seqFrontendBodyOrigin {
	expr = strings.TrimSpace(expr)
	if lit, ok := seqFrontendStringLit(expr); ok {
		return seqFrontendBodyOrigin{kind: "literal", literal: lit}
	}
	if expr == "" || strings.ContainsAny(expr, "[]()") {
		return seqFrontendBodyOrigin{kind: "unknown"}
	}
	parts := strings.Split(expr, ".")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
		if parts[i] == "" {
			return seqFrontendBodyOrigin{kind: "unknown"}
		}
	}
	root := parts[0]
	bind, ok := fn.paramBindings[root]
	if !ok || fn.shadowed[root] {
		return seqFrontendBodyOrigin{kind: "unknown"}
	}
	full := strings.Join(parts, ".")
	if seqFrontendFieldOverwrittenBefore(fn.mutations, full, cutoff) {
		return seqFrontendBodyOrigin{kind: "unknown"}
	}
	path := append(append([]string(nil), bind.path...), parts[1:]...)
	if len(path) == 0 {
		return seqFrontendBodyOrigin{kind: "parameter", parameter: bind.parameter}
	}
	return seqFrontendBodyOrigin{kind: "field_read", parameter: bind.parameter, path: path}
}

// seqFrontendFieldOverwrittenBefore 表达式或其对象前缀是否在 cutoff 之前
// 被本函数改写（请求/调用之后发生的改写不倒推）。
func seqFrontendFieldOverwrittenBefore(mutations []seqFrontendMutation, expr string,
	cutoff int) bool {
	for _, m := range mutations {
		if cutoff > 0 && m.pos >= cutoff {
			continue
		}
		if m.path == expr || strings.HasPrefix(expr, m.path+".") ||
			strings.HasPrefix(m.path, expr+".") {
			return true
		}
	}
	return false
}

var (
	seqFrontendDeclRe   = regexp.MustCompile(`(?m)\b(?:const|let|var)\s+([A-Za-z_$][\w$]*)`)
	seqFrontendAssignRe = regexp.MustCompile(`(?m)([A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)*)\s*(?:\+\+|--|=[^=])`)
)

// seqFrontendBodyOverrides 函数体内的局部声明与字段改写（带位置，用于
// 拒绝借用原形参来源；请求之后的改写不失效请求时来源）。
func seqFrontendBodyOverrides(body string) (map[string]bool, []seqFrontendMutation) {
	shadowed := map[string]bool{}
	for _, m := range seqFrontendDeclRe.FindAllStringSubmatch(body, -1) {
		shadowed[m[1]] = true
	}
	var mutations []seqFrontendMutation
	for _, loc := range seqFrontendAssignRe.FindAllStringSubmatchIndex(body, -1) {
		name := body[loc[2]:loc[3]]
		if name == "method" || name == "body" {
			continue
		}
		mutations = append(mutations, seqFrontendMutation{path: name, pos: loc[0]})
	}
	return shadowed, mutations
}
