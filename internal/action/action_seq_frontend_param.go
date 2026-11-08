package action

// B09e.4：前端形参模式（简单形参 + 对象解构/解构别名）。

import "strings"

// seqFrontendParams `id: string, {a, b: c}: Input` → 每个位置一个名字。
func seqFrontendParams(raw string) []string {
	var out []string
	for _, p := range seqFrontendSplitArgs(raw) {
		p = strings.TrimSpace(seqFrontendParamPattern(p))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// seqFrontendParamPattern 去掉默认值/类型标注（忽略解构/嵌套里的冒号）。
func seqFrontendParamPattern(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	depth := 0
	for i, r := range raw {
		switch r {
		case '{', '[', '(':
			depth++
		case '}', ']', ')':
			if depth > 0 {
				depth--
			}
		case ':', '=':
			if depth == 0 {
				return strings.TrimSpace(raw[:i])
			}
		}
	}
	return raw
}

// seqFrontendParamBinding 形参局部名 → 位置 + 解构路径（B09e.4）。
type seqFrontendParamBinding struct {
	parameter int
	path      []string
}

// seqFrontendParamBindings 解析形参绑定（简单形参 + 对象解构/解构别名）。
func seqFrontendParamBindings(raw string) map[string]seqFrontendParamBinding {
	out := map[string]seqFrontendParamBinding{}
	for pos, p := range seqFrontendSplitArgs(raw) {
		seqFrontendBindParamPattern(strings.TrimSpace(seqFrontendParamPattern(p)), pos, nil, out)
	}
	return out
}

// seqFrontendBindParamPattern 单个形参模式 → 局部名绑定。
func seqFrontendBindParamPattern(pattern string, pos int, prefix []string,
	out map[string]seqFrontendParamBinding) {
	pattern = strings.TrimSpace(strings.TrimSuffix(pattern, "?"))
	if pattern == "" || strings.HasPrefix(pattern, "...") {
		return
	}
	if strings.HasPrefix(pattern, "{") && strings.HasSuffix(pattern, "}") {
		for _, field := range seqFrontendSplitArgs(pattern[1 : len(pattern)-1]) {
			field = strings.TrimSpace(field)
			if field == "" || strings.HasPrefix(field, "...") {
				continue
			}
			key, rest := field, field
			if i := seqFrontendTopLevelColon(field); i >= 0 {
				key = strings.TrimSpace(field[:i])
				rest = strings.TrimSpace(field[i+1:])
			}
			key = strings.TrimSpace(strings.TrimSuffix(key, "?"))
			path := append(append([]string(nil), prefix...), key)
			if strings.HasPrefix(rest, "{") && strings.HasSuffix(rest, "}") {
				seqFrontendBindParamPattern(rest, pos, path, out)
				continue
			}
			local := strings.TrimSpace(seqFrontendParamPattern(rest))
			if local == "" {
				local = key
			}
			out[local] = seqFrontendParamBinding{parameter: pos, path: path}
		}
		return
	}
	if pattern != "" {
		out[pattern] = seqFrontendParamBinding{parameter: pos}
	}
}

// seqFrontendTopLevelColon 形参模式顶层的冒号（忽略嵌套括号）。
func seqFrontendTopLevelColon(s string) int {
	depth := 0
	for i, r := range s {
		switch r {
		case '{', '[', '(':
			depth++
		case '}', ']', ')':
			if depth > 0 {
				depth--
			}
		case ':':
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}
