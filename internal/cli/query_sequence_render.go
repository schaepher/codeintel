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
	// R83：参与者 label 两行——对象名 + 短类型名（<br/>；类型取首个
	// 出现的调用节点）
	typeOf := map[string]string{}
	collectTypes(root.Nodes, typeOf)
	for i, p := range parts {
		alias[p] = fmt.Sprintf("P%d", i)
		label := p
		if t := typeOf[p]; t != "" {
			label = p + "<br/>" + t
		}
		b.WriteString(fmt.Sprintf("  participant %s as %s\n", alias[p], label))
	}
	writeSeqNode(&b, alias, root.Label, root.Nodes)
	return b.String()
}

// collectTypes 收集参与者 → 短类型名（首个出现的调用节点为准）。
func collectTypes(nodes []*codeSeqNode, out map[string]string) {
	for _, n := range nodes {
		if n.Kind == "call" && n.Type != "" && out[n.Actor] == "" {
			out[n.Actor] = n.Type
		}
		collectTypes(n.Nodes, out)
		collectTypes(n.Else, out)
	}
}
// collectParts 收集参与者（R83：调用对象 Actor 去重——s.manager/
// t.repo/ic；消息线 label 保持完整调用名）。
func collectParts(nodes []*codeSeqNode, parts *[]string, seen map[string]bool) {
	for _, n := range nodes {
		if n.Kind == "call" && n.Actor != "" && !seen[n.Actor] {
			seen[n.Actor] = true
			*parts = append(*parts, n.Actor)
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
			to := alias[n.Actor]
			if to == "" {
				to = alias[from]
			}
			// R83：消息线带参数类型短名（Method(ArgType, ...)）
			label := n.Label
			if len(n.Args) > 0 {
				label += "(" + strings.Join(n.Args, ", ") + ")"
			}
			b.WriteString(fmt.Sprintf("  %s->>%s: %s\n", alias[from], to, label))
			if len(n.Nodes) > 0 {
				// R81：嵌套展开——From 切换为被调者 Actor
				writeSeqNode(b, alias, n.Actor, n.Nodes)
			}
			// R83：return 线（返回值类型——虚线返回；plantuml 转 return 语法）
			if len(n.Returns) > 0 {
				b.WriteString(fmt.Sprintf("  %s-->>%s: return %s\n", to, alias[from], strings.Join(n.Returns, ", ")))
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
