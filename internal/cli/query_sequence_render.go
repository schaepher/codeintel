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
	// todo12-13：完整性与投影诊断（mermaid 注释不渲染，但保留可审计行）
	if root.Meta != nil {
		for _, d := range root.Meta.Diagnostics {
			b.WriteString(fmt.Sprintf("%%%% diag %s x%d %s\n", d.Code, d.Count, seqCommentText(d.Detail)))
		}
	}

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
		if t := typeOf[p]; t == seqUnresolvedImplMark {
			label = p + "（实现未消歧）"
		} else if t != "" {
			label = p + "<br/>" + t
		}
		b.WriteString(fmt.Sprintf("  participant %s as %s\n", alias[p], label))
	}
	writeSeqNode(&b, alias, root.Label, "", root.Nodes)
	return b.String()
}

// seqCommentText 注释内容压成单行（换行/制表符会破坏 Mermaid/PlantUML 注释）。
func seqCommentText(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(s)
}

// collectTypes 收集参与者 → 短类型名（首个出现的调用节点为准）。
// P0-5：数据流具体化命中时合并"声明接口 → 数据流实现"双行
// （s.manager 字段声明 IManager、数据流实现 orderManagerImpl）。
// todo13-4：多候选未消歧的接口参与者不得显示成首个实现（标“实现未消歧”）。
func collectTypes(nodes []*action.CodeSeqNode, out map[string]string) {
	for _, n := range nodes {
		if n.Kind == "call" && n.Actor != "" && out[n.Actor] == "" {
			if n.Evidence != nil && n.Evidence.Ambiguous && len(n.Evidence.Candidates) > 0 {
				out[n.Actor] = seqUnresolvedImplMark
			} else if n.Type != "" {
				t := n.Type
				switch {
				case n.DeclType != "" && n.ImplType != "":
					t = n.DeclType + " → " + n.ImplType
				case n.ImplType != "" && n.ImplType != t:
					t += " → " + n.ImplType
				}
				out[n.Actor] = t
			}
		}
		collectTypes(n.Nodes, out)
		collectTypes(n.Else, out)
	}
}

// seqUnresolvedImplMark 接口实现未消歧的参与者标记（Mermaid 参与者标签）。
const seqUnresolvedImplMark = "\x00unresolved-impl"

// seqCandidateShort canonical 候选 ID → 简短身份（(ImplCN).Do → ImplCN.Do）。
func seqCandidateShort(id string) string {
	s := strings.TrimPrefix(id, "symbol:go:")
	if i := strings.LastIndex(s, ":"); i >= 0 {
		s = s[i+1:]
	}
	s = strings.TrimPrefix(s, "(")
	s = strings.Replace(s, ").", ".", 1)
	return s
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
			label := seqExecPrefix(n) + n.Label
			// todo10-3：接口多候选未消歧——不静默当作确定实现
			if n.Evidence != nil && n.Evidence.Ambiguous {
				label = "? " + label
			}
			if len(n.Args) > 0 {
				label += "<br/>(" + strings.Join(n.Args, ", ") + ")"
			} // todo10-8：箭头附证据身份（同一 ID/位置/依据，可在文件里核验；
			// mermaid 注释不参与渲染，仅保留可审计行）
			if ev := seqEvidenceComment(alias[from], to, n); ev != "" {
				b.WriteString(ev)
			}
			// todo10-2：go 用异步箭头（-)）——并发启动，不画同步完成
			if n.Exec == action.SeqExecAsync {
				b.WriteString(fmt.Sprintf("  %s-)%s: %s\n", alias[from], to, label))
				b.WriteString(fmt.Sprintf("  Note over %s,%s: 并发启动（完成顺序未知）\n", alias[from], to))
			} else {
				b.WriteString(fmt.Sprintf("  %s->>%s: %s\n", alias[from], to, label))
			}
			// todo13-4：接口多候选未消歧——完整候选与依据写进图内（不只注
			// 释）；不得把首个实现画成已确定参与者/调用。
			if n.Evidence != nil && n.Evidence.Ambiguous && len(n.Evidence.Candidates) > 0 {
				cands := make([]string, 0, len(n.Evidence.Candidates))
				for _, c := range n.Evidence.Candidates {
					cands = append(cands, seqCandidateShort(c))
				}
				detail := "实现未消歧（unverified）；候选 " + strings.Join(cands, "、")
				if n.Evidence.Reason != "" && n.Evidence.Reason != "unique" {
					detail += "；依据 " + n.Evidence.Reason
				}
				b.WriteString(fmt.Sprintf("  Note over %s,%s: %s\n", alias[from], to, detail))
			}
			if len(n.Nodes) > 0 {
				// R81：嵌套展开——From 切换为被调者 Actor
				writeSeqNode(b, alias, n.Actor, from, n.Nodes)
			}
			// todo10-3：为什么没继续展开（分析停止 vs 业务流程结束）
			if n.Stop != nil && n.Stop.Code != "" && n.Stop.Code != action.SeqStopNoCalls {
				b.WriteString(fmt.Sprintf("  Note over %s,%s: 未继续展开：%s%s\n",
					alias[from], to, n.Stop.Code, seqStopSuffix(n.Stop.Detail)))
			}
			// R83：return 线（返回值类型——虚线返回；plantuml 转 return 语法）
			// todo10-2：并发启动的调用没有"同步完成"语义——不画返回线
			if len(n.Returns) > 0 && n.Exec != action.SeqExecAsync {
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
			if n.Scene != nil && (n.Scene.Status == action.SceneTaken || n.Scene.Status == action.SceneExcluded) {
				b.WriteString(fmt.Sprintf("  Note over %s: 场景判定 %s%s\n", alias[from], n.Scene.Status, seqSceneNoteSuffix(n.Scene)))
			} else if n.Scene != nil && n.Scene.Status == action.SceneUnknown && n.Scene.Cause != "" {
				// todo13-3：值被未知调用改写/深度上限——两个分支保留，但指明原因
				b.WriteString(fmt.Sprintf("  Note over %s: 场景未定（%s）— 两个分支均保留\n", alias[from], seqSceneNoteSuffix(n.Scene)))
			}
			b.WriteString(fmt.Sprintf("  alt %s\n", n.Label))
			writeSeqNode(b, alias, from, caller, n.Nodes)
			if len(n.Else) > 0 {
				b.WriteString("  else\n")
				writeSeqNode(b, alias, from, caller, n.Else)
			}
			b.WriteString("  end\n")
		case "assign":
			// todo12-11：必要赋值（状态初始化/条件改写）在 mermaid 里保留——
			// 与文本/JSON 使用同一节点切片，不另行裁剪；画成 Note 而非远程
			// 调用线（赋值不是调用）。
			label := n.Label
			if n.Line > 0 {
				label = fmt.Sprintf("L%d %s", n.Line, label)
			}
			if n.Focus {
				label += "（焦点）"
			}
			who := alias[from]
			if who == "" {
				who = alias[rootLabel(alias)]
			}
			if who != "" {
				b.WriteString(fmt.Sprintf("  Note over %s: %s\n", who, label))
			}
		case "return":
			// S2：分支内 return → 虚线回调用者；todo10-2：带返回值与结果槽
			b.WriteString(fmt.Sprintf("  %s-->>%s: %s\n", alias[from], alias[from], seqReturnLabel(n)))
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

// seqText 缩进文本渲染 → 字符串（R100：--out 文件输出复用）。

// rootLabel 取参与者 alias 里最小序号的（fallback 用）。

// seqExecPrefix 执行语义前缀（defer=返回时执行 / go=并发启动 / 空=同步）。
