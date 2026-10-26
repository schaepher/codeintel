package cli

import (
	"fmt"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
)

// writeSeqText 缩进文本渲染（默认输出）。
func writeSeqText(nodes []*action.CodeSeqNode, depth int) {
	fmt.Print(seqText(nodes, depth))
}

// seqText 缩进文本渲染 → 字符串（R100：--out 文件输出复用）。
func seqText(nodes []*action.CodeSeqNode, depth int) string {
	var b strings.Builder
	pad := strings.Repeat("  ", depth)
	for _, n := range nodes {
		switch n.Kind {
		case "call":

			b.WriteString(fmt.Sprintf("%s%d. %s%s%s%s\n", pad, n.Line, seqAmbiguousMark(n), seqExecPrefix(n), n.Label,
				seqFocusMark(n)))

			if n.Evidence != nil && n.Evidence.Ambiguous && len(n.Evidence.Candidates) > 0 {
				cands := make([]string, 0, len(n.Evidence.Candidates))
				for _, c := range n.Evidence.Candidates {
					cands = append(cands, seqCandidateShort(c))
				}
				b.WriteString(fmt.Sprintf("%s   候选未消歧: %s（%s）\n", pad, strings.Join(cands, "、"), n.Evidence.Reason))
			}
			if len(n.Nodes) > 0 {
				b.WriteString(seqText(n.Nodes, depth+1))
			}
		case "branch":
			b.WriteString(fmt.Sprintf("%sif %s%s\n", pad, n.Label, seqSceneMark(n)))
			b.WriteString(seqText(n.Nodes, depth+1))
			if len(n.Else) > 0 {
				b.WriteString(fmt.Sprintf("%selse\n", pad))
				b.WriteString(seqText(n.Else, depth+1))
			}
		case "loop":
			b.WriteString(fmt.Sprintf("%sloop %s\n", pad, n.Label))
			b.WriteString(seqText(n.Nodes, depth+1))
		case "return":

			b.WriteString(fmt.Sprintf("%s%d. %s%s\n", pad, n.Line, seqReturnLabel(n), seqFocusMark(n)))
		case "assign":
			b.WriteString(fmt.Sprintf("%s%d. %s%s\n", pad, n.Line, n.Label, seqFocusMark(n)))
		case "continue", "break":
			b.WriteString(fmt.Sprintf("%s%s\n", pad, n.Label))
		}
	}
	return b.String()
}

// rootLabel 取参与者 alias 里最小序号的（fallback 用）。
func rootLabel(alias map[string]string) string {
	best, bestID := "", ""
	for _, id := range alias {
		if bestID == "" || id < bestID {
			bestID = id
		}
	}
	for name, id := range alias {
		if id == bestID {
			best = name
		}
	}
	return best
}

// seqExecPrefix 执行语义前缀（defer=返回时执行 / go=并发启动 / 空=同步）。
func seqExecPrefix(n *action.CodeSeqNode) string {
	switch n.Exec {
	case action.SeqExecDefer:
		return "defer "
	case action.SeqExecAsync:
		return "go "
	}
	return ""
}
