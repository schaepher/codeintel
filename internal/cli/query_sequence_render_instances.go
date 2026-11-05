package cli

// U04g：条件化请求实例的文本/Mermaid 展示（同一展示树；不修改分析结果）。

import (
	"fmt"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
)

// seqWorkflowInstances 实例集合（>1 个实例才走实例视图）。
func seqWorkflowInstances(root *action.CodeSeqNode) []action.SeqWorkflowInstance {
	if root == nil || root.Workflow == nil || len(root.Workflow.Instances) < 2 {
		return nil
	}
	return root.Workflow.Instances
}

// seqInstanceTitle 实例类型中文/契约标签（Mermaid 消息行）。
func seqInstanceTitle(inst action.SeqWorkflowInstance) string {
	if inst.Kind == "selected_root" {
		return "selected root"
	}
	return "conditional external request"
}

// seqInstanceEntryShort 实例入口短名。
func seqInstanceEntryShort(entry string) string {
	if i := strings.LastIndex(entry, ":"); i >= 0 {
		return entry[i+1:]
	}
	return entry
}

// renderCodeSeqInstancesMermaid 实例视图 → Mermaid（外部调用方发起每条请求）。
func renderCodeSeqInstancesMermaid(root *action.CodeSeqNode) string {
	instances := seqWorkflowInstances(root)
	entries := root.Workflow.Entries
	var b strings.Builder
	b.WriteString("sequenceDiagram\n")
	if root.Meta != nil {
		for _, d := range root.Meta.Diagnostics {
			b.WriteString(fmt.Sprintf("%%%% diag %s x%d %s\n", d.Code, d.Count, seqCommentText(d.Detail)))
		}
	}
	// 参与者按入口作用域隔离（与 U07b 同口径）。
	type scopeActor struct{ scope, actor, typ string }
	var ordered []scopeActor
	perEntry := map[string][]scopeActor{}
	shortCount := map[string]int{}
	for _, e := range entries {
		if e.Sequence == nil {
			continue
		}
		actors := []string{e.Sequence.Label}
		seen := map[string]bool{e.Sequence.Label: true}
		collectParts(e.Sequence.Nodes, &actors, seen)
		typeOf := map[string]string{}
		collectTypes(e.Sequence.Nodes, typeOf)
		for _, a := range actors {
			sa := scopeActor{scope: e.Symbol, actor: a, typ: typeOf[a]}
			perEntry[e.Symbol] = append(perEntry[e.Symbol], sa)
			ordered = append(ordered, sa)
			shortCount[a]++
		}
	}
	b.WriteString("  participant P0 as External entries\n")
	alias := map[string]string{}
	next := 1
	for _, sa := range ordered {
		key := sa.scope + "\x00" + sa.actor
		if _, ok := alias[key]; ok {
			continue
		}
		alias[key] = fmt.Sprintf("P%d", next)
		next++
		label := sa.actor
		if shortCount[sa.actor] > 1 {
			if sa.actor == seqEntryShort(sa.scope) {
				label = sa.scope
			} else {
				label = sa.scope + "." + sa.actor
			}
		}
		switch sa.typ {
		case seqUnresolvedImplMark:
			label += "（实现未消歧）"
		case seqUnknownReceiverMark:
			label += "（实例来源未知）"
		case "":
		default:
			label += "<br/>" + sa.typ
		}
		b.WriteString(fmt.Sprintf("  participant %s as %s\n", alias[key], label))
	}
	for _, inst := range instances {
		actors := perEntry[inst.Entry]
		if len(actors) == 0 {
			continue
		}
		rootActor := actors[0].actor
		entryAlias := map[string]string{}
		for _, sa := range actors {
			entryAlias[sa.actor] = alias[sa.scope+"\x00"+sa.actor]
		}
		b.WriteString(fmt.Sprintf("  P0->>%s: %s %s: %s\n", entryAlias[rootActor],
			seqInstanceTitle(inst), inst.ID, seqInstanceEntryShort(inst.Entry)))
		if inst.ParentID != "" {
			b.WriteString(fmt.Sprintf("  Note over P0,%s: 依赖 %s 的写入；请求顺序未证明\n",
				entryAlias[rootActor], inst.ParentID))
		} else {
			b.WriteString(fmt.Sprintf("  Note over P0,%s: 分析起点（entry order unproven）\n",
				entryAlias[rootActor]))
		}
		for _, rv := range inst.ReadValues {
			if rv.Origin == nil || rv.Origin.Literal == "" {
				continue
			}
			b.WriteString(fmt.Sprintf("  Note over %s: 条件化读取值 %s=%s（%s）\n",
				entryAlias[rootActor], rv.Column, rv.Origin.Literal, rv.Origin.Kind))
		}
		for _, key := range seqInstanceKeyNotes(root, inst) {
			b.WriteString(fmt.Sprintf("  Note over %s: %s\n", entryAlias[rootActor], key))
		}
		if len(inst.MissingProofs) > 0 {
			b.WriteString(fmt.Sprintf("  Note over %s: 缺失证明 %s\n",
				entryAlias[rootActor], strings.Join(inst.MissingProofs, ",")))
		}
		b.WriteString("  opt " + seqInstanceTitle(inst) + " " + inst.ID + "（条件化；成立前提未证明）\n")
		seqWriteInstanceNodes(&b, entryAlias, rootActor, inst.Sequence)
		b.WriteString("  end\n")
	}
	return b.String()
}

// seqInstanceKeyNotes 实例来源键（第二次请求的 -service 键来源等）。
func seqInstanceKeyNotes(root *action.CodeSeqNode, inst action.SeqWorkflowInstance) []string {
	if root == nil || root.Workflow == nil || inst.ConnectionIndex < 0 ||
		inst.ConnectionIndex >= len(root.Workflow.Connections) {
		return nil
	}
	conn := root.Workflow.Connections[inst.ConnectionIndex]
	var out []string
	for _, k := range conn.Keys {
		out = append(out, fmt.Sprintf("请求键 %s ← %s（%s → %s，未证明相等）",
			k.Written.Column, seqFormatOrigin(k.Written.Origin), k.Written.Scope, k.Requested.Scope))
	}
	return out
}

// seqFormatOrigin 来源 → 可读文本（concat/parameter/literal）。
func seqFormatOrigin(o *action.SeqStorageOrigin) string {
	if o == nil {
		return "未知"
	}
	switch o.Kind {
	case action.SeqOriginLiteral:
		return o.Literal
	case action.SeqOriginParameter:
		idx := 0
		if o.Parameter != nil {
			idx = *o.Parameter
		}
		return seqInstanceEntryShort(o.Symbol) + fmt.Sprintf(".param[%d]", idx)
	case action.SeqOriginConcat:
		parts := make([]string, 0, len(o.Parts))
		for i := range o.Parts {
			parts = append(parts, seqFormatOrigin(&o.Parts[i]))
		}
		return strings.Join(parts, "+")
	case action.SeqOriginReadColumn:
		if o.ReadColumn != nil {
			return "read(" + o.ReadColumn.Column + ")"
		}
	case action.SeqOriginScanError:
		if o.ScanResult != nil {
			return "scan_error(" + o.ScanResult.ID + ")"
		}
	}
	return o.Kind
}

// seqWriteInstanceNodes 渲染实例树（分支带 instance_condition 真值说明）。
func seqWriteInstanceNodes(b *strings.Builder, alias map[string]string, from string,
	seq *action.CodeSeqNode) {
	if seq == nil {
		return
	}
	for _, n := range seq.Nodes {
		if n == nil {
			continue
		}
		if n.Kind == "branch" && n.InstanceCondition != nil {
			ic := n.InstanceCondition
			label := fmt.Sprintf("  Note over %s: instance condition %s = %s", alias[from],
				seqCommentText(n.Label), ic.Truth)
			if ic.Conflict {
				label += "（instance_fact_conflict）"
			}
			b.WriteString(label + "\n")
		}
		seqWriteInstanceNode(b, alias, from, n)
	}
}

// seqWriteInstanceNode 单节点（call/branch/loop/return 复用 writeSeqNode 语义）。
func seqWriteInstanceNode(b *strings.Builder, alias map[string]string, from string,
	n *action.CodeSeqNode) {
	// 复用 writeSeqNode 的单节点语义：包装成切片避免复制渲染逻辑。
	writeSeqNode(b, alias, from, "", []*action.CodeSeqNode{n})
}

// seqTextRootInstances 实例视图文本。
func seqTextRootInstances(root *action.CodeSeqNode) string {
	instances := seqWorkflowInstances(root)
	if instances == nil {
		return ""
	}
	var b strings.Builder
	for _, inst := range instances {
		b.WriteString(fmt.Sprintf("== %s %s: %s ==\n", seqInstanceTitle(inst), inst.ID,
			seqInstanceEntryShort(inst.Entry)))
		if inst.ParentID != "" {
			b.WriteString("  parent: " + inst.ParentID + "（请求顺序未证明）\n")
		}
		for _, rv := range inst.ReadValues {
			if rv.Origin == nil || rv.Origin.Literal == "" {
				continue
			}
			b.WriteString(fmt.Sprintf("  条件化读取值 %s=%s（%s）\n", rv.Column,
				rv.Origin.Literal, rv.Origin.Kind))
		}
		for _, key := range seqInstanceKeyNotes(root, inst) {
			b.WriteString("  " + key + "\n")
		}
		if len(inst.MissingProofs) > 0 {
			b.WriteString("  缺失证明 " + strings.Join(inst.MissingProofs, ",") + "\n")
		}
		if inst.Sequence != nil {
			b.WriteString(seqText(inst.Sequence.Nodes, 1))
			// 条件真值说明（文本与 Mermaid 同信息）。
			var walk func(nodes []*action.CodeSeqNode)
			walk = func(nodes []*action.CodeSeqNode) {
				for _, n := range nodes {
					if n.Kind == "branch" && n.InstanceCondition != nil {
						b.WriteString(fmt.Sprintf("    instance condition %s = %s\n",
							n.Label, n.InstanceCondition.Truth))
					}
					walk(n.Nodes)
					walk(n.Else)
				}
			}
			walk(inst.Sequence.Nodes)
		}
	}
	b.WriteString("entry order unproven：实例数量不代表请求已经发生\n")
	return b.String()
}
