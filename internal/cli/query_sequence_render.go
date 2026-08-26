package cli

import (
	"fmt"
	"strings"
)

// renderCodeSeqMermaid 代码级步骤树 → mermaid sequenceDiagram
// （参与者 = 调用目标 + 入口；消息线 = 调用名；branch → alt/else；
// loop → loop 块）。
func renderCodeSeqMermaid(root *codeSeqNode) string {
	if root == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("sequenceDiagram\n")

	parts := []string{root.Label}
	seen := map[string]bool{root.Label: true}
	collectParts(root.Nodes, &parts, seen)
	// R83：参与者按出现顺序声明（mermaid 后声明者靠右）——调用方先
	// 出现靠左、被调者靠右，箭头尽量从左到右（此前字母排序导致
	// 指向 GetMyCart 的线从右到左——用户实测）
	alias := map[string]string{}
	for i, p := range parts {
		alias[p] = fmt.Sprintf("P%d", i)
		b.WriteString(fmt.Sprintf("  participant %s as %s\n", alias[p], p))
	}
	writeSeqNode(&b, alias, root.Label, root.Nodes)
	return b.String()
}
// collectParts 收集参与者（调用目标去重）。
func collectParts(nodes []*codeSeqNode, parts *[]string, seen map[string]bool) {
	for _, n := range nodes {
		if n.Kind == "call" && !seen[n.Label] {
			seen[n.Label] = true
			*parts = append(*parts, n.Label)
		}
		collectParts(n.Nodes, parts, seen)
		collectParts(n.Else, parts, seen)
	}
}
// writeSeqNode 递归渲染节点块（call 消息 / branch alt / loop）。
func writeSeqNode(b *strings.Builder, alias map[string]string, from string, nodes []*codeSeqNode) {
	for _, n := range nodes {
		switch n.Kind {
		case "call":
			to := alias[n.Label]
			if to == "" {
				to = alias[from]
			}
			b.WriteString(fmt.Sprintf("  %s->>%s: %s\n", alias[from], to, n.Label))
			if len(n.Nodes) > 0 {

				writeSeqNode(b, alias, n.Label, n.Nodes)
			}
		case "branch":
			b.WriteString(fmt.Sprintf("  alt %s\n", n.Label))
			writeSeqNode(b, alias, from, n.Nodes)
			if len(n.Else) > 0 {
				b.WriteString("  else\n")
				writeSeqNode(b, alias, from, n.Else)
			}
			b.WriteString("  end\n")
		case "loop":
			b.WriteString(fmt.Sprintf("  loop %s\n", n.Label))
			writeSeqNode(b, alias, from, n.Nodes)
			b.WriteString("  end\n")
		}
	}
}
// writeSeqText 缩进文本渲染（默认输出）。
func writeSeqText(nodes []*codeSeqNode, depth int) {
	pad := strings.Repeat("  ", depth)
	for _, n := range nodes {
		switch n.Kind {
		case "call":
			fmt.Printf("%s%d. %s\n", pad, n.Line, n.Label)
			if len(n.Nodes) > 0 {
				writeSeqText(n.Nodes, depth+1) // 嵌套展开（--depth >1）
			}
		case "branch":
			fmt.Printf("%sif %s\n", pad, n.Label)
			writeSeqText(n.Nodes, depth+1)
			if len(n.Else) > 0 {
				fmt.Printf("%selse\n", pad)
				writeSeqText(n.Else, depth+1)
			}
		case "loop":
			fmt.Printf("%sloop %s\n", pad, n.Label)
			writeSeqText(n.Nodes, depth+1)
		}
	}
}
