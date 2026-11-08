package action

// 前端函数模型构造（从 action_seq_frontend_parse.go 拆出）。

import "strings"

// seqFrontendFuncFromSrc 从源码片段构造函数模型（命名导出/默认导出共用）。
func seqFrontendFuncFromSrc(src, rel, name, paramsRaw string, start int) seqFrontendFunc {
	line := 1 + strings.Count(src[:start], "\n")
	body := seqFrontendBody(src, start)
	fn := seqFrontendFunc{name: name, file: rel, line: line,
		params: seqFrontendParams(paramsRaw), paramBindings: seqFrontendParamBindings(paramsRaw)}
	fn.shadowed, fn.mutated = seqFrontendBodyOverrides(body)
	if m := seqFrontendFetchArgRe.FindStringSubmatch(body); m != nil {
		arg := strings.TrimSpace(m[1])
		if lit, ok := seqFrontendStringLit(arg); ok {
			fn.path = lit
		} else {
			fn.fetchArg = arg
		}
	}
	if m := seqFrontendMethodRe.FindStringSubmatch(body); m != nil {
		fn.method = strings.ToUpper(m[1])
	} else if fn.path != "" {
		fn.method = "GET"
	}
	fn.body = seqFrontendParseBody(body)
	for _, m := range seqFrontendCallRe.FindAllStringSubmatch(body, -1) {
		call := m[1]
		if call == "fetch" || call == "function" || call == "if" || call == "return" ||
			call == "JSON" || call == "stringify" {
			continue
		}
		fn.calls = append(fn.calls, seqFrontendCall{name: call,
			args: seqFrontendSplitArgs(m[2])})
	}
	return fn
}
