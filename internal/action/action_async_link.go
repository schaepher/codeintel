package action

// todo10-6：异步关联的因果判据（从 action_async.go 拆出——行数治理）。
//
// 三条判据 + "仅同作用域不算因果"：
//   (a) field:<F>   一方写、另一方读/写同名字段 → corroborated
//   (b) call_chain  两者都在 seed 的调用链上 → corroborated
//   (c) id_field:<col> 同名 ID 类字段 → corroborated
//   (none) shared_scope_only → unverified（明确不构成因果，但不隐藏）

import "strings"

// asyncLink 两个入口的关联判定（Q3 的三条判据 + 仅同作用域的 unverified）。
func asyncLink(x, y AsyncNode, facts map[string]*asyncFnFacts, chain map[string]bool) (AsyncLink, bool) {
	link := AsyncLink{From: x.ID, To: y.ID}
	// (b) 调用链可达（两者都在 chainSymbols 的链上——seed 自身也标记在链里，
	// 故必须是"双方都在"，否则 seed 与任意节点的对都会被误判为 call_chain）
	if chain[x.ID] && chain[y.ID] {
		link.Reason, link.Verdict = "call_chain", SeqVerdictCorroborated
		link.Detail = "同一调用链（calls 边可达）"
		return link, true
	}
	fx, fy := facts[x.ID], facts[y.ID]
	if fx == nil || fy == nil {
		return link, false
	}
	// (a) 字段链路：一方写、另一方读/写同名字段
	for p := range fx.writes {
		if fy.reads[p] || fy.writes[p] {
			link.Reason, link.Verdict = "field:"+p, SeqVerdictCorroborated
			link.Detail = "状态字段读写关联（" + p + "）——值流可达性未逐跳验证"
			return link, true
		}
	}
	// (c) 标识同源：同名 ID 类字段
	for p := range allFields(fx) {
		if !asyncIDLike(fieldLeaf(p)) {
			continue
		}
		for q := range allFields(fy) {
			if fieldLeaf(q) == fieldLeaf(p) {
				link.Reason, link.Verdict = "id_field:"+fieldLeaf(p), SeqVerdictCorroborated
				link.Detail = "同名标识字段（" + fieldLeaf(p) + "）"
				return link, true
			}
		}
	}
	// 仅同作用域：明确标注不构成因果
	sx, sy := asyncScopeOf(allFields(fx)), asyncScopeOf(allFields(fy))
	if sx != "" && sx == sy {
		link.Reason, link.Verdict = "shared_scope_only", SeqVerdictUnverified
		link.Detail = "只共享作用域（类型/表），不构成因果证据"
		return link, true
	}
	// 同一包（无字段摘要时）：同样只是"作用域"，标 unverified
	if asyncPkgPrefix(x.ID) == asyncPkgPrefix(y.ID) {
		link.Reason, link.Verdict = "shared_scope_only", SeqVerdictUnverified
		link.Detail = "只共享包作用域，不构成因果证据"
		return link, true
	}
	return link, false
}

// allFields 函数涉及的字段集合（读 + 写）。
func allFields(f *asyncFnFacts) map[string]bool {
	out := map[string]bool{}
	if f == nil {
		return out
	}
	for p := range f.writes {
		out[p] = true
	}
	for p := range f.reads {
		out[p] = true
	}
	return out
}

// fieldLeaf 字段路径末段。
func fieldLeaf(p string) string {
	if i := strings.LastIndex(p, "."); i >= 0 {
		return p[i+1:]
	}
	return p
}

// asyncIDLike 末段是否 ID 类字段（*_id / *Id / id / *_no / *_code）。
func asyncIDLike(leaf string) bool {
	l := strings.ToLower(leaf)
	return l == "id" || strings.HasSuffix(l, "_id") || strings.HasSuffix(l, "id") ||
		strings.HasSuffix(l, "_no") || strings.HasSuffix(l, "_code")
}

// asyncPkgPrefix 从 canonical ID 取"同包"前缀（symbol:go:<pkg>:）。
func asyncPkgPrefix(id string) string {
	i := strings.LastIndex(id, ":")
	if i < 0 {
		return ""
	}
	return id[:i+1]
}
