package cli

import (
	"fmt"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// entitySequenceMermaid 实体级时序图（R12）：函数级调用链（有序）
// 投影到实体——保留调用顺序、连续相同实体对合并计数（×N）、实体
// 内调用折叠（内互调已在节点标注）。参与者带包前缀消歧。
func entitySequenceMermaid(g *domain.EntityGraph, steps []domain.WikiSeqStep) string {
	if g == nil || len(g.ByName) == 0 {
		return ""
	}
	// 1. steps → 实体对序列（保留顺序；无映射/实体内调用跳过）；
	//    记录每个实体的被调函数名（消息行展示 call 了什么）
	type pair struct{ from, to string }
	var seq []pair
	var calleeNames []string	// 与 seq 平行：每条边的被调函数短名
	firstEntity := func(sym string) string {
		for _, eid := range g.ByName[sym] {
			return eid
		}
		return ""
	}
	for _, st := range steps {
		from, to := firstEntity(st.Caller), firstEntity(st.Callee)
		if from == "" || to == "" || from == to {
			continue
		}
		seq = append(seq, pair{from, to})
		calleeNames = append(calleeNames, st.Callee)
	}
	if len(seq) == 0 {
		return ""
	}
	// 2. 合并连续重复（保留顺序；被调函数名去重收集）
	var merged []struct {
		pair
		count	int
		names	[]string
	}
	for i, p := range seq {
		if n := len(merged); n > 0 && merged[n-1].pair == p {
			merged[n-1].count++
			if !containsStr(merged[n-1].names, calleeNames[i]) {
				merged[n-1].names = append(merged[n-1].names, calleeNames[i])
			}
		} else {
			merged = append(merged, struct {
				pair
				count	int
				names	[]string
			}{p, 1, []string{calleeNames[i]}})
		}
	}

	entityShort := func(id string) string {
		for _, n := range g.Nodes {
			if n.ID == id {
				if n.Kind == domain.EntityKindPkgFace {
					return shortMod(n.Pkg) + "（门面" + fmt.Sprint(n.FreeFuncs) + "）"
				}
				return shortMod(n.Pkg) + ":" + n.Name
			}
		}
		return id
	}
	alias := map[string]string{}
	var order []string
	aliasOf := func(id string) string {
		if a, ok := alias[id]; ok {
			return a
		}
		a := fmt.Sprintf("P%d", len(order))
		alias[id] = a
		order = append(order, id)
		return a
	}
	var b strings.Builder
	b.WriteString("sequenceDiagram\n")

	seen := map[string]bool{}
	for _, m := range merged {
		for _, id := range []string{m.from, m.to} {
			if !seen[id] {
				seen[id] = true
				aliasOf(id)
			}
		}
	}
	for _, id := range order {
		b.WriteString(fmt.Sprintf("  participant %s as \"%s\"\n", alias[id], entityShort(id)))
	}
	for _, m := range merged {

		label := strings.Join(m.names, ", ")
		if len(m.names) > 3 {
			label = strings.Join(m.names[:3], ", ") + fmt.Sprintf(" 等 %d 个", len(m.names))
		}
		if m.count > 1 {
			label += fmt.Sprintf(" ×%d", m.count)
		}
		b.WriteString(fmt.Sprintf("  %s->>%s: %s\n", alias[m.from], alias[m.to], label))
	}
	return b.String()
}
