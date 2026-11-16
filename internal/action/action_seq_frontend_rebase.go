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
			// 被调函数相对其形参的字段路径（合并前，用于调用点改写判定）。
			calleePath := append([]string(nil), o.path...)
			// 被调路径接在调用实参来源之后（解构/字段路径不丢）。
			o.parameter = rebased.parameter
			o.path = append(append([]string(nil), rebased.path...), o.path...)
			o.local = ""
			if len(o.path) > 0 {
				o.kind = "field_read"
			} else {
				o.kind = "parameter"
			}
			o.literal = rebased.literal
			// 调用方在调用之前的改写使来源失效（请求之后的改写不倒推）。
			// 表达式取调用方真实书写形式（解构别名/字段路径），根名不按
			// map 遍历挑一个别名。
			if expr := rebased.local; expr != "" {
				full := expr
				if len(calleePath) > 0 {
					full += "." + strings.Join(calleePath, ".")
				}
				if seqFrontendFieldOverwrittenBefore(caller.mutations, full, callPos) {
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
