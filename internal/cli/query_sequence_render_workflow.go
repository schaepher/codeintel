package cli

// todo30 U04a/U07b：关联入口集合的展示（同一分析树；渲染只读）。
//
// 参与者按**入口作用域**隔离：不同入口的同名函数/局部变量不是同一个图
// 参与者。入口短名冲突时使用准确 canonical ID（`symbol:...:Run`），局部
// 变量用 `<入口ID>.<actor>`。默认单入口输出的参与者保持不变。

import (
	"fmt"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
)

// seqWorkflowEntries 关联入口集合（>1 个入口才走集合展示；否则返回 nil）。
func seqWorkflowEntries(root *action.CodeSeqNode) []action.SeqWorkflowEntry {
	if root == nil || root.Workflow == nil || len(root.Workflow.Entries) < 2 {
		return nil
	}
	return root.Workflow.Entries
}

// seqTextRoot 代码级时序文本（单入口保持原格式；关联入口逐入口展示）。
func seqTextRoot(root *action.CodeSeqNode) string {
	if root == nil {
		return ""
	}
	// B10e：文本与 JSON/Mermaid 共用同一份前端动作事实。
	prefix := seqFrontendText(root)
	// U04g：实例视图（>1 实例）优先。
	if txt := seqTextRootInstances(root); txt != "" {
		return prefix + txt
	}
	entries := seqWorkflowEntries(root)
	if entries == nil {
		return prefix + fmt.Sprintf("== %s 代码级时序 ==\n", root.Label) + seqText(root.Nodes, 1) +
			seqDiagText(root) + seqSceneText(root)
	}
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(fmt.Sprintf("== %s 代码级时序（entry order unproven） ==\n",
			seqEntryShort(e.Symbol)))
		if e.Sequence == nil {
			b.WriteString("  （入口分析树缺失）\n")
			continue
		}
		b.WriteString(seqText(e.Sequence.Nodes, 1))
		b.WriteString(seqDiagText(e.Sequence))
		b.WriteString(seqSceneText(e.Sequence))
	}
	b.WriteString("entry order unproven：入口集合不表示请求顺序或执行次数\n")
	return prefix + b.String()
}

// seqEntryShort 入口 canonical ID → 短名（末段）。
func seqEntryShort(id string) string {
	if i := strings.LastIndex(id, ":"); i >= 0 {
		return id[i+1:]
	}
	return id
}

// seqScopedActor 一个入口作用域内的参与者（身份 = 入口 ID + 源码 actor）。
type seqScopedActor struct {
	scope string // 入口 canonical ID
	actor string // 源码里的参与者名（函数名/对象名）
	typ   string // 短类型名（参与者标签第二行）
}

// renderCodeSeqWorkflowMermaid 关联入口 → 单一 sequenceDiagram（参与者按
// 入口作用域隔离；每个入口标注 entry order unproven）。
func renderCodeSeqWorkflowMermaid(root *action.CodeSeqNode) string {
	entries := seqWorkflowEntries(root)
	if entries == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("sequenceDiagram\n")
	if root.Meta != nil {
		for _, d := range root.Meta.Diagnostics {
			b.WriteString(fmt.Sprintf("%%%% diag %s x%d %s\n", d.Code, d.Count, seqCommentText(d.Detail)))
		}
	}
	// 收集每个入口的参与者（出现顺序：入口自身 → 调用目标）。
	var ordered []seqScopedActor
	perEntry := make([][]seqScopedActor, len(entries))
	shortCount := map[string]int{}
	for i, e := range entries {
		if e.Sequence == nil {
			continue
		}
		actors := []string{e.Sequence.Label}
		seen := map[string]bool{e.Sequence.Label: true}
		collectParts(e.Sequence.Nodes, &actors, seen)
		typeOf := map[string]string{}
		collectTypes(e.Sequence.Nodes, typeOf)
		for _, a := range actors {
			sa := seqScopedActor{scope: e.Symbol, actor: a, typ: typeOf[a]}
			perEntry[i] = append(perEntry[i], sa)
			ordered = append(ordered, sa)
			shortCount[a]++
		}
	}
	// 参与者声明（P0 = 外部入口，避免把后端调用画成前一个后端直接调用）。
	b.WriteString("  participant P0 as External entries\n")
	alias := map[string]string{} // 作用域键 → Pn
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
			// 入口短名冲突：使用准确 canonical ID（入口自身）或入口作用域的
			// 局部身份（<入口ID>.<actor>）。
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
	// B10e：前端动作与请求必须在同一张图里（不能只在图下注释补名字）。
	seqRenderFrontendMermaid(&b, root.Workflow, alias)
	// 每个入口一个独立作用域块；顺序只按入口集合（entry order unproven）。
	for i, e := range entries {
		if e.Sequence == nil {
			continue
		}
		entryAlias := map[string]string{}
		for _, sa := range perEntry[i] {
			entryAlias[sa.actor] = alias[sa.scope+"\x00"+sa.actor]
		}
		rootActor := e.Sequence.Label
		b.WriteString(fmt.Sprintf("  Note over P0,%s: entry %s — entry order unproven\n",
			entryAlias[rootActor], seqEntryShort(e.Symbol)))
		writeSeqNode(&b, entryAlias, rootActor, "", e.Sequence.Nodes)
	}
	return b.String()
}
