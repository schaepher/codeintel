package action

// todo22 P2：变量链回溯（写入来源的 Ident 递归）——从 action_seq_dataflow.go
// 拆出（行数治理）+ 请求内缓存（键含文件/变量/深度，深浅预算不互相污染）。

import "go/ast"

// varChainTypes 变量链回溯：找 "v := RHS" / "v = RHS" 赋值 → 递归解析
// RHS（多赋值处都收集——条件分支形态）。
func varChainTypes(a *Actions, req CodeSequenceRequest, f *ast.File, imports map[string]string, name string, depth int, curPkg string) []writeSource {
	cacheKey := curPkg + "\x00" + varChainFileKey(req, f) + "\x00" + name + "\x00" + itoaSeq(depth)
	if v, ok := req.varChains[cacheKey]; ok {
		return v
	}
	var out []writeSource
	ast.Inspect(f, func(node ast.Node) bool {
		as, ok := node.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		id, ok := as.Lhs[0].(*ast.Ident)
		if !ok || id.Name != name {
			return true
		}
		out = appendUnique(out, rhsTypes(a, req, f, imports, as.Rhs[0], depth, curPkg)...)
		return true
	})
	if req.varChains != nil {
		req.varChains[cacheKey] = out
	}
	return out
}

// varChainFileKey 文件缓存键（AST 指针 + 文件名；同一请求内稳定的文件身份）。
func varChainFileKey(req CodeSequenceRequest, f *ast.File) string {
	if f == nil || f.Name == nil {
		return ""
	}
	return f.Name.Name + "@" + itoaSeq(int(f.Package))
}

// appendUnique 去重追加（同 typeName+pkgPath 只保留一个）。
func appendUnique(out []writeSource, more ...writeSource) []writeSource {
	for _, m := range more {
		dup := false
		for _, e := range out {
			if e == m {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, m)
		}
	}
	return out
}
