package cli

// B10e：把 workflow.frontend（与 JSON 同一份分析事实）画进文本/Mermaid。
//
// 规则：
//   - 已证明动作 → 动作参与者 + 请求消息（方法/路径）+ 请求体来源 + 准确 handler；
//   - 目标未证明 → 明确说明原因，绝不伪造成"已证明的调用"，也不删掉事件；
//   - 动作顺序不代表按钮/请求的真实执行顺序（源码扫描顺序 ≠ 业务顺序）。

import (
	"fmt"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
)

// seqFrontendHandlerAlias 入口在 alias 表里的参与者别名（作用域键隔离）。
func seqFrontendHandlerAlias(alias map[string]string, entry string) string {
	if entry == "" {
		return ""
	}
	prefix := entry + "\x00"
	keys := make([]string, 0, 2)
	for k := range alias {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	short := seqEntryShort(entry)
	for _, k := range keys {
		if strings.TrimPrefix(k, prefix) == short {
			return alias[k]
		}
	}
	return alias[keys[0]]
}

// seqFrontendActionNote 动作的请求体来源说明（与 request_body 同源）。
func seqFrontendActionNote(a action.SeqFrontendAction) string {
	if len(a.RequestBody) == 0 {
		return ""
	}
	names := make([]string, 0, len(a.RequestBody))
	for name := range a.RequestBody {
		names = append(names, name)
	}
	sort.Strings(names)
	var parts []string
	for _, name := range names {
		v := a.RequestBody[name]
		switch v.Kind {
		case "parameter":
			idx := 0
			if v.Parameter != nil {
				idx = *v.Parameter
			}
			parts = append(parts, fmt.Sprintf("%s ← 形参 %d", name, idx))
		case "literal":
			parts = append(parts, fmt.Sprintf("%s=%s", name, v.Literal))
		default:
			parts = append(parts, name+" ← 未知")
		}
	}
	return "请求体 " + strings.Join(parts, "，")
}

// seqRenderFrontendMermaid 前端动作 → Mermaid 参与者/消息（同一 workflow 事实）。
func seqRenderFrontendMermaid(b *strings.Builder, wf *action.SeqWorkflow,
	alias map[string]string) {
	if wf == nil || wf.Frontend == nil {
		return
	}
	actions := wf.Frontend.Actions
	if len(actions) == 0 {
		b.WriteString("  Note over P0: 前端动作缺失（源码范围内未找到可证明的事件绑定）\n")
		return
	}
	for i, a := range actions {
		if a.Unknown != "" {
			b.WriteString(fmt.Sprintf("  Note over P0: 前端动作 %s 目标未知（%s）\n",
				seqCommentText(a.Action), a.Unknown))
			continue
		}
		handler := seqFrontendHandlerAlias(alias, a.Entry)
		if handler == "" {
			b.WriteString(fmt.Sprintf("  Note over P0: 前端动作 %s 已识别（%s %s），后端 handler 未在入口集合中\n",
				seqCommentText(a.Action), seqCommentText(a.Method), seqCommentText(a.Path)))
			continue
		}
		falias := fmt.Sprintf("F%d", i+1)
		b.WriteString(fmt.Sprintf("  participant %s as %s\n", falias, seqCommentText(a.Action)))
		b.WriteString(fmt.Sprintf("  %s->>%s: %s %s\n", falias, handler,
			seqCommentText(a.Method), seqCommentText(a.Path)))
		if note := seqFrontendActionNote(a); note != "" {
			b.WriteString(fmt.Sprintf("  Note over %s,%s: %s\n", falias, handler,
				seqCommentText(note)))
		}
	}
	b.WriteString("  Note over P0: 前端动作顺序不代表真实请求顺序\n")
}

// seqFrontendText 前端动作的文本视图（与 JSON/Mermaid 同一份事实）。
func seqFrontendText(root *action.CodeSeqNode) string {
	if root == nil || root.Workflow == nil || root.Workflow.Frontend == nil {
		return ""
	}
	actions := root.Workflow.Frontend.Actions
	var b strings.Builder
	b.WriteString("== frontend actions（源码动作，顺序不代表业务顺序） ==\n")
	if len(actions) == 0 {
		b.WriteString("  前端动作缺失（源码范围内未找到可证明的事件绑定）\n")
		return b.String()
	}
	for _, a := range actions {
		if a.Unknown != "" {
			b.WriteString(fmt.Sprintf("  %s 目标未知（%s）\n", a.Action, a.Unknown))
			continue
		}
		handler := seqEntryShort(a.Entry)
		if handler == "" {
			handler = "handler 未在入口集合中"
		}
		b.WriteString(fmt.Sprintf("  %s %s %s → %s", a.Action, a.Method, a.Path, handler))
		if note := seqFrontendActionNote(a); note != "" {
			b.WriteString("（" + note + "）")
		}
		b.WriteString("\n")
	}
	return b.String()
}
