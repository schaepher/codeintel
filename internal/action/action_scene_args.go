package action

import "go/ast"

// sceneArgValues 实参解析结果 → (值, 来源, 未知原因) 三切片。
func sceneArgValues(args []sceneArg) ([]string, []string, []string) {
	vals := make([]string, 0, len(args))
	srcs := make([]string, 0, len(args))
	reasons := make([]string, 0, len(args))
	for _, a := range args {
		vals = append(vals, a.val)
		srcs = append(srcs, a.source)
		reasons = append(reasons, a.reason)
	}
	return vals, srcs, reasons
}

// paramReasonArgs 形参名 → 未知原因（与 paramLiteralArgs 位置对齐）。
func paramReasonArgs(fl *ast.FieldList, lits, reasons []string) map[string]string {
	if fl == nil || len(lits) == 0 {
		return nil
	}
	out := map[string]string{}
	idx := 0
	for _, f := range fl.List {
		n := len(f.Names)
		if n == 0 {
			n = 1
		}
		for i := 0; i < n; i++ {
			if idx < len(lits) && lits[idx] != "" && idx < len(reasons) && reasons[idx] != "" {
				name := ""
				if i < len(f.Names) {
					name = f.Names[i].Name
				}
				if name != "" {
					out[name] = reasons[idx]
				}
			}
			idx++
		}
	}
	return out
}

// paramSourceArgs 形参名 → 值来源（与 paramLiteralArgs 位置对齐）。
func paramSourceArgs(fl *ast.FieldList, lits, srcs []string) map[string]string {
	if fl == nil || len(lits) == 0 {
		return nil
	}
	out := map[string]string{}
	idx := 0
	for _, f := range fl.List {
		n := len(f.Names)
		if n == 0 {
			n = 1
		}
		for i := 0; i < n; i++ {
			if idx < len(lits) && lits[idx] != "" {
				name := ""
				if i < len(f.Names) {
					name = f.Names[i].Name
				}
				if name != "" && idx < len(srcs) {
					out[name] = srcs[idx]
				}
			}
			idx++
		}
	}
	return out
}

// paramLiteralArgs 形参名 → 实参字面量（按签名位置对齐；跳过 receiver）。
func paramLiteralArgs(fl *ast.FieldList, lits []string) map[string]string {
	if fl == nil || len(lits) == 0 {
		return nil
	}
	out := map[string]string{}
	idx := 0
	for _, f := range fl.List {
		n := len(f.Names)
		if n == 0 {
			n = 1
		}
		for i := 0; i < n; i++ {
			if idx < len(lits) && lits[idx] != "" {
				name := ""
				if i < len(f.Names) {
					name = f.Names[i].Name
				}
				if name != "" {
					out[name] = lits[idx]
				}
			}
			idx++
		}
	}
	return out
}

// appendUniqueStr 追加去重（保持入参顺序——诊断文本稳定）。
func appendUniqueStr(out []string, v string) []string {
	for _, s := range out {
		if s == v {
			return out
		}
	}
	return append(out, v)
}

// copyStrMap 浅拷贝字符串 map（nil 安全——缺失键语义保持"没有该键"）。
func copyStrMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
