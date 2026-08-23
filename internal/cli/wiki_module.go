package cli

import (
	"fmt"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// wikiAutoDesc 自动推断描述（F）：yaml 描述/包注释都空时的结构化
// fallback——只陈述代码事实（核心符号/调用方向），不编造业务含义。
// 渲染层三处共用（md/html/serve 导航）。
func wikiAutoDesc(wm *domain.WikiModule) string {
	var parts []string
	if len(wm.CoreSymbols) > 0 {
		names := make([]string, 0, 3)
		for i, s := range wm.CoreSymbols {
			if i >= 3 {
				break
			}
			names = append(names, s.Name)
		}
		parts = append(parts, "核心符号 "+strings.Join(names, "、"))
	}
	if len(wm.OutCalls) > 0 {
		parts = append(parts, fmt.Sprintf("调用 %d 个模块", len(wm.OutCalls)))
	}
	if len(wm.InCalls) > 0 {
		parts = append(parts, fmt.Sprintf("被 %d 个模块调用", len(wm.InCalls)))
	}
	if len(parts) == 0 {
		return ""
	}
	return "（自动推断）" + strings.Join(parts, "；")
}

// moduleDesc 模块描述三级取用（yaml → 包注释 → 自动推断）。
func moduleDesc(wm *domain.WikiModule, metaDesc string) string {
	if metaDesc != "" {
		return metaDesc
	}
	if wm.Desc != "" {
		return wm.Desc
	}
	return wikiAutoDesc(wm)
}

// renderModulePage 模块页六区块 + 架构图 + 流程时序。
func renderModulePage(wm *domain.WikiModule, desc string, tableAlias map[string]string, hidden map[string]bool, cfg wikiConfig) string {
	var b strings.Builder
	b.WriteString("# " + wm.Name + "\n\n")
	if desc != "" {
		b.WriteString("> " + desc + "\n\n")
	}
	b.WriteString("## 职责\n\n")
	if desc != "" {
		b.WriteString(desc + "（来源：wiki.yaml）\n\n")
	}
	if wm.Desc != "" {
		b.WriteString(wm.Desc + "（来源：包注释）\n\n")
	}
	if desc == "" && wm.Desc == "" {
		if ad := wikiAutoDesc(wm); ad != "" {
			b.WriteString(ad + "\n\n")
		} else {
			b.WriteString("（无描述——维护者可在 wiki.yaml modules.description 补充）\n\n")
		}
	}
	if len(wm.Entries) > 0 {
		b.WriteString("## 入口\n\n")
		for _, e := range wm.Entries {
			b.WriteString("- `" + e + "`\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("## 核心符号（内部实现参考——被调用最多）\n\n")
	if len(wm.CoreSymbols) > 0 {
		b.WriteString("| 符号 | 类型 | 调用者数 | 位置 |\n|---|---|---|---|\n")
		for _, s := range wm.CoreSymbols {
			if hidden[s.Name] {
				continue
			}
			loc := ""
			if s.File != "" {
				loc = fmt.Sprintf("%s:%d", s.File, s.Line)
			}
			b.WriteString(fmt.Sprintf("| %s | %s | %d | %s |\n", s.Name, s.Kind, s.Callers, loc))
		}
		b.WriteString("\n")
	} else {
		b.WriteString("（无调用数据）\n\n")
	}
	if len(wm.OutCalls) > 0 {
		b.WriteString("## 调用的模块\n\n")
		for _, m := range wm.OutCalls {
			b.WriteString("- `" + m + "`\n")
		}
		b.WriteString("\n")
	}
	if len(wm.InCalls) > 0 {
		b.WriteString("## 被哪些模块调用\n\n")
		for _, m := range wm.InCalls {
			b.WriteString("- `" + m + "`\n")
		}
		b.WriteString("\n")
	}
	if len(wm.Tables) > 0 {
		b.WriteString("## 相关表\n\n")
		for _, t := range wm.Tables {
			line := "- [`" + t + "`](tables.md#" + t + ")"
			if a := tableAlias[t]; a != "" {
				line += "（" + a + "）"
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("## 架构图（包间调用）\n\n")
	arch := moduleArchMermaid(wm)
	if arch != "" {
		b.WriteString("```mermaid\n" + arch + "\n```\n\n")
	} else {
		b.WriteString("（单模块或无线索；整体架构见 index 架构图）\n\n")
	}

	b.WriteString("## 流程时序\n\n")
	hasSeq := false
	for _, f := range cfg.Flows {
		b.WriteString("### " + f.Title + "\n\n")
		b.WriteString("```mermaid\n" + f.Mermaid + "\n```\n\n")
		hasSeq = true
	}
	if len(wm.Flows) > 0 {
		b.WriteString("（自动生成：内部调用链——代码事实，帮助理解模块怎么运转；业务时序见上方 yaml flows）\n\n")
	}
	for _, fl := range wm.Flows {
		b.WriteString("### 内部调用链：" + fl.Title + "\n\n")
		b.WriteString("```mermaid\n" + sequenceMermaid(fl.Steps) + "\n```\n\n")
		hasSeq = true
	}
	if !hasSeq {
		b.WriteString("（无调用链——yaml flows 可手写业务时序）\n\n")
	}
	return b.String()
}

// moduleArchMermaid 模块页架构图（Q251-A：包间调用图——calls 边按
// 包聚合，线上标调用次数；替代空模块级 gRPC 图）。
func moduleArchMermaid(wm *domain.WikiModule) string {
	if len(wm.PkgCalls) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("graph LR\n")
	for _, c := range wm.PkgCalls {
		b.WriteString(fmt.Sprintf("  %s -->|%d| %s\n", archNode(c.From), c.Count, archNode(c.To)))
	}
	return b.String()
}

// archNode mermaid 节点（Q251 补：`[cli]` 纯方括号是非法语法——
// mermaid 要求 id[文本] 形态；id 用短名保证唯一）。
func archNode(name string) string {
	return name + "[" + name + "]"
}

// shortMod module 路径末段（渲染用）。
func shortMod(mod string) string {
	if i := strings.LastIndex(mod, "/"); i >= 0 {
		return mod[i+1:]
	}
	return mod
}

// sequenceMermaid 调用链 → sequenceDiagram（参与者 + 边，确定性排序）。
// sequenceMermaid 自动时序（Q251 补）：参与者含括号符号名
// （(Actions).BatchSymbols）直接出现在消息行是语法错误——参与者
// 别名化（P0/P1… + participant P0 as "显示名"），消息行用别名。
func sequenceMermaid(steps []domain.WikiSeqStep) string {
	var b strings.Builder
	b.WriteString("sequenceDiagram\n")
	alias := map[string]string{}
	var order []string
	for _, st := range steps {
		for _, p := range []string{st.Caller, st.Callee} {
			if _, ok := alias[p]; !ok {
				alias[p] = fmt.Sprintf("P%d", len(order))
				order = append(order, p)
			}
		}
	}
	for _, p := range order {
		b.WriteString(fmt.Sprintf("  participant %s as \"%s\"\n", alias[p], p))
	}
	for _, st := range steps {
		b.WriteString(fmt.Sprintf("  %s->>%s: call\n", alias[st.Caller], alias[st.Callee]))
	}
	return b.String()
}
