package action

// B09e.4：包装函数来源重绑定（实参根对象 + 路径 + 调用方改写失效）。

import "strings"

// seqFrontendRebaseBody 把被调函数体内的形参/字段来源替换为调用点来源，
// 并应用调用方在调用之前发生的字段改写（B09e.4）。
func seqFrontendRebaseBody(body map[string]seqFrontendBodyOrigin, calleeParams []string,
	argOrigins map[string]seqFrontendBodyOrigin, caller seqFrontendFunc,
	callPos int) map[string]seqFrontendBodyOrigin {
	if len(body) == 0 {
		return nil
	}
	out := map[string]seqFrontendBodyOrigin{}
	for name, o := range body {
		if (o.kind != "parameter" && o.kind != "field_read") ||
			o.parameter < 0 || o.parameter >= len(calleeParams) {
			out[name] = o
			continue
		}
		rebased, ok := argOrigins[calleeParams[o.parameter]]
		if !ok {
			o = seqFrontendBodyOrigin{kind: "unknown"}
		} else if rebased.kind == "parameter" {
			// 被调路径接在调用实参来源之后（解构/字段路径不丢）。
			o.parameter = rebased.parameter
			o.path = append(append([]string(nil), rebased.path...), o.path...)
			if len(o.path) > 0 {
				o.kind = "field_read"
			} else {
				o.kind = "parameter"
			}
			o.literal = rebased.literal
			// 调用方在调用之前的改写/遮蔽使来源失效（请求之后的改写不倒推）。
			if base := seqFrontendParamBaseName(caller, o.parameter); base != "" {
				full := base
				if len(o.path) > 0 {
					full += "." + strings.Join(o.path, ".")
				}
				if caller.shadowed[base] ||
					seqFrontendFieldOverwrittenBefore(caller.mutations, full, callPos) {
					o = seqFrontendBodyOrigin{kind: "unknown"}
				}
			}
		} else if len(o.path) == 0 && len(rebased.path) == 0 {
			o = rebased
		} else {
			o = seqFrontendBodyOrigin{kind: "unknown"}
		}
		out[name] = o
	}
	return out
}

// seqFrontendParamBaseName 调用方第 idx 个形参的根名（解构形参取绑定根）。
func seqFrontendParamBaseName(fn seqFrontendFunc, idx int) string {
	if idx < 0 || idx >= len(fn.params) {
		return ""
	}
	name := strings.TrimSpace(fn.params[idx])
	if strings.HasPrefix(name, "{") || strings.HasPrefix(name, "[") {
		for local, b := range fn.paramBindings {
			if b.parameter == idx {
				return local
			}
		}
		return ""
	}
	return name
}
