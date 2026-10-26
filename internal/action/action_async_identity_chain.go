package action

import "strings"

// asyncIdentityChains 由关联证据构建逐跳身份链（写 → 边界 → 读/过滤）。
// 绑定关系本身来自 asyncLink（值来源/字段共享），这里只把"值从哪里写、
// 经过什么边界、被谁读/过滤"展开成可核对的行。
func (a *Actions) asyncIdentityChains(links []AsyncLink, nodes []AsyncNode,
	facts map[string]*asyncFnFacts) []AsyncChain {
	if len(links) == 0 {
		return nil
	}
	fileOf := map[string]string{}
	lineOf := map[string]int{}
	for _, n := range nodes {
		fileOf[n.ID] = n.File
		lineOf[n.ID] = n.Line
	}
	colIdx := a.tableColumnIndex()
	var out []AsyncChain
	seen := map[string]bool{}
	for _, l := range links {
		kind, field := splitAsyncReason(l.Reason)
		if field == "" {
			continue
		}
		key := l.From + "\x00" + l.To + "\x00" + field
		if seen[key] {
			continue
		}
		seen[key] = true
		wfn, wline, wfield := asyncFieldSide(facts, l.From, l.To, field, true)
		rfn, rline, rfield := asyncFieldSide(facts, l.From, l.To, field, false)
		if wfn == "" || rfn == "" {
			continue
		}
		hops := []AsyncHop{{
			Kind: "write", Function: wfn, Field: wfield,
			File: fileOf[wfn], Line: wline, Verdict: SeqVerdictCorroborated,
			Detail: "字段写入（SSA 摘要 direct/indirect write）",
		}}
		leaf := fieldLeaf(wfield)
		if table, column, ok := colIdx.lookup(leaf); ok {
			hops = append(hops, AsyncHop{Kind: "persistence", Field: table + "." + column,
				Verdict: SeqVerdictUnverified,
				Detail:  "字段→表列映射（列名匹配）；同一列不代表同一条记录"})
		} else {
			hops = append(hops, AsyncHop{Kind: "persistence_or_queue", Field: leaf,
				Verdict: SeqVerdictUnverified,
				Detail:  "未建模持久化/消息边界（静态仅见字段身份）"})
		}
		hops = append(hops, AsyncHop{
			Kind: "read", Function: rfn, Field: rfield,
			File: fileOf[rfn], Line: rline, Verdict: SeqVerdictCorroborated,
			Detail: "字段读取（SSA 摘要）",
		})
		hops = append(hops, AsyncHop{Kind: "filter", Function: rfn, Field: rfield,
			Verdict: SeqVerdictUnverified,
			Detail:  "过滤/查询条件未建模（无法证明读到同一条记录）"})
		verdict := SeqVerdictUnverified
		reason := l.EvidenceKind
		if l.Verdict == SeqVerdictCorroborated && (kind == "value_origin" || kind == "call_path") {
			verdict = SeqVerdictCorroborated
		}
		out = append(out, AsyncChain{From: l.From, To: l.To, Field: wfield,
			Hops: hops, Verdict: verdict, Reason: reason})
	}
	return out
}

// splitAsyncReason "value_origin:field" → (kind, field)。
func splitAsyncReason(reason string) (string, string) {
	kind := reason
	field := ""
	if i := strings.Index(reason, ":"); i >= 0 {
		kind, field = reason[:i], reason[i+1:]
	}
	return kind, field
}

// asyncFieldSide 在 from/to 两侧找字段的写入方（write=true）或读取方。
func asyncFieldSide(facts map[string]*asyncFnFacts, from, to, field string, write bool) (fn string, line int, full string) {
	for _, id := range []string{from, to} {
		f := facts[id]
		if f == nil {
			continue
		}
		set, lines := f.reads, f.rlines
		if write {
			set, lines = f.writes, f.wlines
		}
		for p := range set {
			if p == field || fieldLeaf(p) == field {
				return id, lines[p], p
			}
		}
	}
	return "", 0, ""
}
