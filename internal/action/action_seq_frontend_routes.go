package action

// 前端动作 → 后端路由表（从 action_seq_frontend_parse.go 拆出）。

import (
	"path/filepath"
	"strings"
)

// seqFrontendLookup 按文件/导入解析函数身份（同名不跨文件覆盖）。
func seqFrontendLookup(name, file string, funcs map[string]seqFrontendFunc,
	imports map[string]map[string]seqFrontendImport) (seqFrontendFunc, bool) {
	return seqFrontendLookupDepth(name, file, funcs, imports, 0)
}

// seqFrontendLookupDepth 沿导入/重导出链解析（B09e.2：可证明的重导出；
// 深度有界，环即失败——不按扫描顺序挑一个）。
func seqFrontendLookupDepth(name, file string, funcs map[string]seqFrontendFunc,
	imports map[string]map[string]seqFrontendImport, depth int) (seqFrontendFunc, bool) {
	if depth > 8 {
		return seqFrontendFunc{}, false
	}
	if fn, ok := funcs[file+"\x00"+name]; ok {
		return fn, true
	}
	if imp, ok := imports[file][name]; ok && strings.HasPrefix(imp.path, ".") {
		if rel := seqFrontendImportFile(file, imp.path, funcs, imports); rel != "" {
			exported := imp.exported
			if exported == "" {
				exported = name
			}
			if fn, ok := seqFrontendLookupDepth(exported, rel, funcs, imports, depth+1); ok {
				return fn, true
			}
		}
	}
	return seqFrontendFunc{}, false
}

// seqFrontendImportFile 相对导入路径 → 已解析文件（尝试常见扩展名）。
//
// B09e：只做重导出（没有导出函数）的文件也要能被解析——只看函数表会把
// `export {default} from './a'` 这类桥接文件当成不存在。
func seqFrontendImportFile(cur, imp string, funcs map[string]seqFrontendFunc,
	imports map[string]map[string]seqFrontendImport) string {
	base := strings.TrimSuffix(filepath.ToSlash(filepath.Dir(cur)), "/")
	rel := filepath.ToSlash(filepath.Join(base, imp))
	for _, ext := range []string{"", ".ts", ".tsx", ".js", ".jsx"} {
		cand := rel + ext
		for key := range funcs {
			if strings.HasPrefix(key, cand+"\x00") {
				return cand
			}
		}
		if _, ok := imports[cand]; ok {
			return cand
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
