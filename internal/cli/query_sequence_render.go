package cli

// R95：代码级时序渲染（mermaid/缩进文本）留 cli——节点类型与解析
// 逻辑在 action（action.CodeSeqNode / Actions.CodeSequence）。

import (
	"fmt"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
)

// renderCodeSeqMermaid 代码级步骤树 → mermaid sequenceDiagram
// （参与者 = 调用目标 + 入口；消息线 = 调用名；branch → alt/else；
// loop → loop 块）。
func renderCodeSeqMermaid(root *action.CodeSeqNode) string {
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
	writeSeqNode(&b, alias, root.Label, "", root.Nodes)
	return b.String()
}

// collectTypes 收集参与者 → 短类型名（首个出现的调用节点为准）。
// P0-5：数据流具体化命中时合并"声明接口 → 数据流实现"双行
// （s.manager 字段声明 IManager、数据流实现 orderManagerImpl）。
func collectTypes(nodes []*action.CodeSeqNode, out map[string]string) {
	for _, n := range nodes {
		if n.Kind == "call" && n.Type != "" && out[n.Actor] == "" {
			t := n.Type
			switch {
			case n.DeclType != "" && n.ImplType != "":
				t = n.DeclType + " → " + n.ImplType
			case n.ImplType != "" && n.ImplType != t:
				t += " → " + n.ImplType
			}
			out[n.Actor] = t
		}
		collectTypes(n.Nodes, out)
		collectTypes(n.Else, out)
	}
}

// collectParts 收集参与者（R83：调用对象 Actor 去重——s.manager/
// t.repo/ic；消息线 label 保持完整调用名）。R83：跳过 Go 基础类型
// 参与者（int64(x) 类型转换——非调用）；匿名函数/变量函数保留。
func collectParts(nodes []*action.CodeSeqNode, parts *[]string, seen map[string]bool) {
	for _, n := range nodes {
		if n.Kind == "call" && n.Actor != "" && !seqBaseTypeActor(n.Actor) && !seen[n.Actor] {
			seen[n.Actor] = true
			*parts = append(*parts, n.Actor)
		}
		collectParts(n.Nodes, parts, seen)
		collectParts(n.Else, parts, seen)
	}
}

// seqBaseTypeActor 参与者是否为 Go 基础类型（int64(x) 类型转换等——
// 非真实调用，图上无意义）。
func seqBaseTypeActor(actor string) bool {
	if action.SigTypeKeyword(actor) {
		return true
	}
	// 类型转换带括号/泛型形态（int64(x) 的 Actor 是纯类型名）
	return strings.HasPrefix(actor, "[]") || strings.HasPrefix(actor, "map[") ||
		strings.HasPrefix(actor, "func(") || strings.HasPrefix(actor, "chan ")
}

// hasRenderableSeqNode 递归检查节点树是否有可渲染内容（R100：基础类型
// actor 的 call 消息渲染时被跳过——空分支检查须排除它们，否则 alt/end
// 之间无中间行的空块；return/continue/break 线可渲染）。
func hasRenderableSeqNode(nodes []*action.CodeSeqNode) bool {
	for _, n := range nodes {
		switch n.Kind {
		case "call":
			// 基础类型 actor（int32(x) 转换）消息被跳过；嵌套展开仍可渲染
			if !seqBaseTypeActor(n.Actor) || hasRenderableSeqNode(n.Nodes) {
				return true
			}
		case "branch":
			if hasRenderableSeqNode(n.Nodes) || hasRenderableSeqNode(n.Else) {
				return true
			}
		case "loop":
			if hasRenderableSeqNode(n.Nodes) {
				return true
			}
		default: // return/continue/break
			return true
		}
	}
	return false
}

// writeSeqNode 递归渲染节点块（call 消息 / branch alt / loop）。
func writeSeqNode(b *strings.Builder, alias map[string]string, from, caller string, nodes []*action.CodeSeqNode) {
	for _, n := range nodes {
		switch n.Kind {
		case "call":
			// R83：基础类型参与者（int64 类型转换）跳过消息——非调用
			if seqBaseTypeActor(n.Actor) {
				continue
			}
			to := alias[n.Actor]
			if to == "" {
				to = alias[from]
			}
			// R83：消息线参数类型单独第二行（Method<br/>(ArgType, ...)）
			label := n.Label
			if len(n.Args) > 0 {
				label += "<br/>(" + strings.Join(n.Args, ", ") + ")"
			}
			b.WriteString(fmt.Sprintf("  %s->>%s: %s\n", alias[from], to, label))
			if len(n.Nodes) > 0 {
				// R81：嵌套展开——From 切换为被调者 Actor
				writeSeqNode(b, alias, n.Actor, from, n.Nodes)
			}
			// R83：return 线（返回值类型——虚线返回；plantuml 转 return 语法）
			if len(n.Returns) > 0 {
				b.WriteString(fmt.Sprintf("  %s-->>%s: return %s\n", to, alias[from], strings.Join(n.Returns, ", ")))
			}
		case "branch":
			// S2：空分支（无 then 无 else）不输出 alt 块——避免渲染问题
			// R100：预过滤可渲染内容——基础类型 actor 的 call 消息会被
			// 跳过（如 if 内只执行 A = int32(util.GetTime())），len>0
			// 但实际无行——递归检查避免 alt 和 end 之间无中间行
			if !hasRenderableSeqNode(n.Nodes) && !hasRenderableSeqNode(n.Else) {
				continue
			}
			b.WriteString(fmt.Sprintf("  alt %s\n", n.Label))
			writeSeqNode(b, alias, from, caller, n.Nodes)
			if len(n.Else) > 0 {
				b.WriteString("  else\n")
				writeSeqNode(b, alias, from, caller, n.Else)
			}
			b.WriteString("  end\n")
		case "return":
			// S2：分支内 return → 虚线回调用者
			b.WriteString(fmt.Sprintf("  %s-->>%s: return\n", alias[from], alias[from]))
		case "continue":
			// S2：循环内 continue → 回循环继续（参与者自回环）
			b.WriteString(fmt.Sprintf("  %s-->>%s: continue\n", alias[from], alias[from]))
		case "break":
			b.WriteString(fmt.Sprintf("  %s-->>%s: break\n", alias[from], alias[from]))
		case "loop":
			// R100：空循环体（无可渲染内容）不输出 loop 块——与 branch 同语义
			if !hasRenderableSeqNode(n.Nodes) {
				continue
			}
			b.WriteString(fmt.Sprintf("  loop %s\n", n.Label))
			writeSeqNode(b, alias, from, caller, n.Nodes)
			b.WriteString("  end\n")
		}
	}
}

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
			b.WriteString(fmt.Sprintf("%s%d. %s\n", pad, n.Line, n.Label))
			if len(n.Nodes) > 0 {
				b.WriteString(seqText(n.Nodes, depth+1)) // 嵌套展开（--depth >1）
			}
		case "branch":
			b.WriteString(fmt.Sprintf("%sif %s\n", pad, n.Label))
			b.WriteString(seqText(n.Nodes, depth+1))
			if len(n.Else) > 0 {
				b.WriteString(fmt.Sprintf("%selse\n", pad))
				b.WriteString(seqText(n.Else, depth+1))
			}
		case "loop":
			b.WriteString(fmt.Sprintf("%sloop %s\n", pad, n.Label))
			b.WriteString(seqText(n.Nodes, depth+1))
		case "return", "continue", "break":
			b.WriteString(fmt.Sprintf("%s%s\n", pad, n.Label))
		}
	}
	return b.String()
}
