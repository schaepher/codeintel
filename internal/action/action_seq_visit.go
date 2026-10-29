package action

// todo23 §2.5：防环键与回调上下文（从 action_seq_funcvalue_exec.go 拆出）。

import (
	"sort"
	"strings"
)

// seqVisitKey 防环键：repo + 符号 + 入站回调上下文（todo23 §2.5）。
// 不能只用符号 ID（同一函数经不同回调路径应可继续展开），也不能用剩余
// 深度/每层新对象地址（会制造假身份、失去防环）。
func seqVisitKey(req CodeSequenceRequest, symID string) string {
	return req.RepoAbs + "|" + symID + "|" + req.callbackCtx
}

// seqCallbackVisitCtx 函数值的词法身份 + 绑定站点（回调上下文）。
func seqCallbackVisitCtx(fv seqValue) string {
	base := ""
	switch fv.kind {
	case seqValueLit:
		base = "lit:" + seqLitKey(fv)
	case seqValueNamed:
		base = "fn:" + fv.symID
	default:
		return ""
	}
	if fv.bindFile != "" {
		base += "@" + fv.bindFile + ":" + itoaSeq(fv.bindLine)
	}
	if fv.declObj != nil && fv.declObj.Name != "" {
		base += "#" + fv.declObj.Name
	}
	return base
}

// T03：函数字面量的展开键要包含**有界捕获环境**——同一个 AST 字面量（如
// 工厂 return 的 `func(){process()}`）在不同绑定下是不同的回调实例
// （`Use(Use(Hit))` 的多层包装），不能按字面量位置判成 cycle。键不依赖
// 指针（对象地址）或剩余 depth：对象名 + 绑定值身份（递归有界）。
func seqLitVisitKey(v seqValue) string {
	base := seqLitKey(v)
	if base == "" {
		return ""
	}
	return base + "|env{" + seqEnvCaptureKey(v.env, 2) + "}"
}

// seqEnvCaptureKey 环境的捕获绑定签名（对象名 → 值身份；确定性排序，有界）。
func seqEnvCaptureKey(env *seqEnv, depth int) string {
	if env == nil || depth < 0 {
		return ""
	}
	type pair struct{ k, v string }
	seen := map[string]string{}
	var order []string
	for e := env; e != nil; e = e.parent {
		var pairs []pair
		for obj, val := range e.vals {
			if obj == nil {
				continue
			}
			pairs = append(pairs, pair{k: obj.Name, v: seqValueVisitKey(val, depth-1)})
		}
		// 内层覆盖外层：先收集内层，外层的同名键跳过。
		sort.Slice(pairs, func(i, j int) bool { return pairs[i].k < pairs[j].k })
		for _, p := range pairs {
			if _, ok := seen[p.k]; ok {
				continue
			}
			seen[p.k] = p.v
			order = append(order, p.k)
		}
	}
	sort.Strings(order)
	var b []string
	for i, k := range order {
		if i >= 8 {
			b = append(b, "…+"+itoaSeq(len(order)-8))
			break
		}
		b = append(b, k+"="+seen[k])
	}
	return strings.Join(b, ",")
}

// seqValueVisitKey 值的身份（有界递归；不依赖指针/剩余 depth）。
func seqValueVisitKey(v seqValue, depth int) string {
	if depth < 0 {
		return "…"
	}
	switch v.kind {
	case seqValueLit:
		if k := seqLitKey(v); k != "" {
			return "lit:" + k + "{" + seqEnvCaptureKey(v.env, depth-1) + "}"
		}
		return "lit:?"
	case seqValueNamed:
		return "fn:" + v.symID
	case seqValueList:
		var parts []string
		for i, e := range v.elems {
			if i >= 8 {
				parts = append(parts, "…+"+itoaSeq(len(v.elems)-8))
				break
			}
			parts = append(parts, seqValueVisitKey(e, depth-1))
		}
		return "list[" + strings.Join(parts, ",") + "]"
	case seqValueMulti:
		var parts []string
		for i, c := range v.cands {
			if i >= 8 {
				parts = append(parts, "…+"+itoaSeq(len(v.cands)-8))
				break
			}
			parts = append(parts, seqValueVisitKey(c, depth-1))
		}
		return "multi{" + strings.Join(parts, "|") + "}"
	case seqValueUnknown:
		return "unknown"
	}
	return "?"
}
