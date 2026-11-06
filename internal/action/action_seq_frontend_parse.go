package action

// 前端源码解析辅助（从 action_seq_frontend.go 拆出）。

import (
	"path/filepath"
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
func seqFrontendResolveFetch(fn seqFrontendFunc, funcs map[string]seqFrontendFunc,
	imports map[string]map[string]string, bindings map[string]string, depth int) (string, string, bool) {
	if depth > 3 {
		return "", "", false
	}
	if fn.fetchArg != "" {
		if p, ok := seqFrontendEvalArg(fn.fetchArg, bindings); ok {
			m := fn.method
			if m == "" {
				m = "GET"
			}
			return p, m, true
		}
	}
	if fn.path != "" {
		m := fn.method
		if m == "" {
			m = "GET"
		}
		return fn.path, m, true
	}
	for _, c := range fn.calls {
		next, ok := seqFrontendLookup(c.name, fn.file, funcs, imports)
		if !ok {
			continue
		}
		nb := map[string]string{}
		for i, arg := range c.args {
			if i >= len(next.params) {
				break
			}
			if v, ok := seqFrontendEvalArg(arg, bindings); ok {
				nb[next.params[i]] = v
			}
		}
		if p, m, ok2 := seqFrontendResolveFetch(next, funcs, imports, nb, depth+1); ok2 {
			return p, m, true
		}
	}
	return "", "", false
}

// seqFrontendLookup 按文件/导入解析函数身份（同名不跨文件覆盖）。
func seqFrontendLookup(name, file string, funcs map[string]seqFrontendFunc,
	imports map[string]map[string]string) (seqFrontendFunc, bool) {
	if fn, ok := funcs[file+"\x00"+name]; ok {
		return fn, true
	}
	if imp := imports[file][name]; strings.HasPrefix(imp, ".") {
		if rel := seqFrontendImportFile(file, imp, funcs); rel != "" {
			if fn, ok := funcs[rel+"\x00"+name]; ok {
				return fn, true
			}
		}
	}
	return seqFrontendFunc{}, false
}

// seqFrontendImportFile 相对导入路径 → 已解析文件（尝试常见扩展名）。
func seqFrontendImportFile(cur, imp string, funcs map[string]seqFrontendFunc) string {
	base := strings.TrimSuffix(filepath.ToSlash(filepath.Dir(cur)), "/")
	rel := filepath.ToSlash(filepath.Join(base, imp))
	for _, ext := range []string{"", ".ts", ".tsx", ".js", ".jsx"} {
		cand := rel + ext
		for key := range funcs {
			if strings.HasPrefix(key, cand+"\x00") {
				return cand
			}
		}
	}
	return ""
}

// seqFrontendRouteMap 后端路由表 → "METHOD path" → handler id（HandleFunc 空
// method 匹配所有方法）。
func seqFrontendRouteMap(a *Actions) map[string][]string {
	out := map[string][]string{}
	nodes, err := a.repo.GetHTTPRouteNodes()
	if err != nil {
		return out
	}
	for _, n := range nodes {
		p := n.Property("path")
		h := n.Property("handler_id")
		if p == "" || h == "" {
			continue
		}
		m := strings.ToUpper(n.Property("method"))
		if m == "" {
			for _, verb := range []string{"GET", "POST", "PUT", "DELETE", "PATCH"} {
				out[verb+" "+p] = append(out[verb+" "+p], h)
			}
			continue
		}
		out[m+" "+p] = append(out[m+" "+p], h)
	}
	return out
}
