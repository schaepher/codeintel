package cli

// 代码级时序渲染辅助（todo10-2/10-3）：执行语义前缀、return 线文本、
// 焦点/歧义标记、展开终点诊断清单。从 query_sequence_render.go 拆出
// （行数治理）。

import (
	"fmt"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
)

// seqReturnLabel return 线文本：返回值 + 结果槽（槽名与值文本重复时不重复标）。
func seqReturnLabel(n *action.CodeSeqNode) string {
	if n == nil {
		return "return"
	}
	label := n.Label
	if label == "" {
		label = "return"
	}
	if len(n.Slots) > 0 && !slotsRedundant(n.Values, n.Slots) {
		label += "   [slots: " + strings.Join(n.Slots, ", ") + "]"
	}
	return label
}

// slotsRedundant 结果槽是否只是值文本的重复（避免 "return a, b [slots: a, b]"）。
func slotsRedundant(values, slots []string) bool {
	if len(values) != len(slots) {
		return false
	}
	for i := range values {
		if strings.TrimSpace(values[i]) != strings.TrimSpace(slots[i]) {
			return false
		}
	}
	return true
}

// seqFocusMark 焦点命中标记（--focus 过滤时提示"为什么这条在这"）。
func seqFocusMark(n *action.CodeSeqNode) string {
	if n != nil && n.Focus {
		return "   ← focus"
	}
	return ""
}

// seqAmbiguousMark 多候选未消歧标记（渲染层可见——不把"第一个候选"当结论）。
func seqAmbiguousMark(n *action.CodeSeqNode) string {
	if n != nil && n.Evidence != nil && n.Evidence.Ambiguous {
		return "? "
	}
	return ""
}

// seqStopSuffix 诊断附加说明（空则不追加）。
func seqStopSuffix(detail string) string {
	if detail == "" {
		return ""
	}
	return "（" + detail + "）"
}

// seqDiagText 展开终点诊断清单（todo10-3：文本模式附在树后——区分
// "分析停止"与"业务流程结束"，并给出源码定位）。
func seqDiagText(root *action.CodeSeqNode) string {
	if root == nil {
		return ""
	}
	var b strings.Builder
	var walk func(nodes []*action.CodeSeqNode, depth int)
	walk = func(nodes []*action.CodeSeqNode, depth int) {
		pad := strings.Repeat("  ", depth)
		for _, n := range nodes {
			if n.Stop != nil && n.Stop.Code != "" && n.Stop.Code != action.SeqStopNoCalls {
				b.WriteString(fmt.Sprintf("%s- %s:%d %s → %s%s\n", pad, seqEvidenceFile(n), n.Line,
					n.Label, n.Stop.Code, seqStopSuffix(n.Stop.Detail)))
			}
			if n.Evidence != nil && n.Evidence.Ambiguous {
				b.WriteString(fmt.Sprintf("%s- %s:%d %s → 多候选未消歧（%d 个候选，选中第 1 个）\n",
					pad, seqEvidenceFile(n), n.Line, n.Label, len(n.Evidence.Candidates)))
			}
			walk(n.Nodes, depth+1)
			walk(n.Else, depth+1)
		}
	}
	walk(root.Nodes, 1)
	if b.Len() == 0 {
		return ""
	}
	return "\n-- 展开终点诊断 --\n" + b.String()
}

// seqEvidenceFile 节点证据里的文件（调用点文件优先，其次符号定义文件）。
func seqEvidenceFile(n *action.CodeSeqNode) string {
	if n == nil || n.Evidence == nil {
		return "?"
	}
	if n.Evidence.CallFile != "" {
		return n.Evidence.CallFile
	}
	if n.Evidence.File != "" {
		return n.Evidence.File
	}
	return "?"
}

// seqEvidenceComment mermaid 证据注释行（%% 开头——渲染忽略、文件可审计）。
func seqEvidenceComment(from, to string, n *action.CodeSeqNode) string {
	if n == nil || n.Evidence == nil {
		return ""
	}
	ev := n.Evidence
	line := fmt.Sprintf("%s:%d", ev.CallFile, ev.CallLine)
	if ev.CallFile == "" {
		line = "-"
	}
	id := ev.SymbolID
	if id == "" {
		id = "-"
	}
	return fmt.Sprintf("  %%%% evidence %s->%s @ %s id=%s verdict=%s\n", from, to, line, id, ev.Verdict)
}

// seqEvidenceText 证据清单（--evidence：逐箭头核验来源——档案式输出）。
func seqEvidenceText(root *action.CodeSeqNode) string {
	if root == nil {
		return ""
	}
	var b strings.Builder
	var walk func(nodes []*action.CodeSeqNode, depth int)
	walk = func(nodes []*action.CodeSeqNode, depth int) {
		pad := strings.Repeat("  ", depth)
		for _, n := range nodes {
			if n.Kind == "call" && n.Evidence != nil {
				ev := n.Evidence
				b.WriteString(fmt.Sprintf("%s- %s:%d %s → source=%s verdict=%s",
					pad, seqEvidenceFile(n), n.Line, n.Label, ev.Source, ev.Verdict))
				if ev.SymbolID != "" {
					b.WriteString(" id=" + ev.SymbolID)
				}
				if ev.Confidence > 0 {
					b.WriteString(fmt.Sprintf(" conf=%.2f", ev.Confidence))
				}
				if len(ev.Candidates) > 0 {
					b.WriteString(fmt.Sprintf(" candidates=%d", len(ev.Candidates)))
				}
				if ev.Query != "" {
					b.WriteString("（" + ev.Query + "）")
				}
				b.WriteString("\n")
			}
			walk(n.Nodes, depth+1)
			walk(n.Else, depth+1)
		}
	}
	walk(root.Nodes, 1)
	if b.Len() == 0 {
		return ""
	}
	return "\n-- 箭头证据 --\n" + b.String()
}
