package cli

// R2 系统级流程页（进程视角）：命令 → 入口函数 → 调用链（索引 callees
// 自动）→ 涉及包——新人看「codeintel init 跑起来发生了什么」。
// 数据源均为代码事实：入口清单映射 root.go Main switch，调用链来自
// 索引（GetCallees），不依赖 AI。

import (
	"fmt"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
)

// processEntry 一个系统流程（命令 → 入口函数名——root.go Main switch）。
type processEntry struct {
	Cmd   string // 展示名
	Entry string // 入口函数名（按名称查索引，跨仓库通用）
}

// processEntries 关键流程清单（新人视角的系统运转主链路）。
var processEntries = []processEntry{
	{"init —— 全量构建索引", "cmdInit"},
	{"update —— 增量更新索引", "cmdUpdate"},
	{"serve —— 图探索 Web 服务", "cmdServe"},
	{"query —— 符号/字段/表查询", "cmdQuery"},
	{"wiki —— 业务 wiki 生成", "cmdWiki"},
	{"mcp —— MCP server（Agent 调用）", "cmdMCP"},
	{"before —— 改动影响预判", "cmdBefore"},
	{"trace —— 数据来龙去脉", "cmdTrace"},
	{"batch —— 批量符号概览", "cmdBatch"},
	{"export —— 字段索引导出", "cmdExport"},
	{"precompute relations —— 表间关联预计算", "cmdPrecompute"},
	{"list —— 全局注册台账", "cmdList"},
}

// procChain 一条流程的调用链（入口 + 边 + 涉及包）。
type procChain struct {
	Entry string // 入口符号名
	Steps []domain.WikiSeqStep
	Pkgs  []string
}

// queryChain 查询入口符号的深度 2 调用链 + 涉及包（短名展示）。
func queryChain(acts *action.Actions, entryName string) *procChain {
	// action 层：ResolveSymbol 名称解析 + Callees 调用链
	entry, err := acts.ResolveSymbol(entryName)
	if err != nil {
		return nil
	}
	facts, err := acts.Callees(entry.ID, 2)
	if err != nil {
		return nil
	}
	chain := &procChain{Entry: shortSymbolName(entry)}
	pkgs := map[string]bool{}
	pkgs[symbolPkg(string(entry.ID))] = true
	seen := map[string]bool{}
	for _, f := range facts {
		caller, callee := shortSymbolNameID(string(f.SourceID)), shortSymbolNameID(string(f.TargetID))
		if !seen[caller+"->"+callee] {
			seen[caller+"->"+callee] = true
			chain.Steps = append(chain.Steps, domain.WikiSeqStep{Caller: caller, Callee: callee})
		}
		pkgs[symbolPkg(string(f.SourceID))] = true
		pkgs[symbolPkg(string(f.TargetID))] = true
	}
	for p := range pkgs {
		if p != "" {
			chain.Pkgs = append(chain.Pkgs, p)
		}
	}
	sort.Strings(chain.Pkgs)
	return chain
}

// shortSymbolName 符号短名（(T).m / 函数名）。
func shortSymbolName(e *domain.CodeEntity) string {
	return shortSymbolNameID(string(e.ID))
}

// shortSymbolNameID canonical ID → 短名（末段，方法带 receiver）。
func shortSymbolNameID(id string) string {
	i := strings.LastIndex(id, ":")
	if i < 0 {
		return id
	}
	return id[i+1:]
}

// symbolPkg canonical ID → 包路径（symbol:go:<pkg>:<name>）。
func symbolPkg(id string) string {
	rest := strings.TrimPrefix(id, "symbol:go:")
	if i := strings.LastIndex(rest, ":"); i >= 0 {
		return rest[:i]
	}
	return rest
}

// renderProcessesMD 流程页 Markdown（每流程：命令 + 调用链 mermaid +
// 涉及包）。
func renderProcessesMD(acts *action.Actions) string {
	var b strings.Builder
	b.WriteString("# 系统流程\n\n> 数据源：命令入口（root.go Main switch 事实映射）+ 索引调用链\n> （GetCallees 深度 2）——看每个命令跑起来调用什么。\n\n")
	for _, pe := range processEntries {
		chain := queryChain(acts, pe.Entry)
		b.WriteString("## " + pe.Cmd + "\n\n")
		if chain == nil || len(chain.Steps) == 0 {
			b.WriteString("（索引中无调用链——可能未重建索引）\n\n")
			continue
		}
		b.WriteString("入口：`" + chain.Entry + "`\n\n")
		b.WriteString("```mermaid\n" + sequenceMermaid(chain.Steps) + "\n```\n\n")
		if len(chain.Pkgs) > 0 {
			b.WriteString("涉及包：`" + strings.Join(chain.Pkgs, "`、`") + "`\n\n")
		}
	}
	return b.String()
}

// renderProcessesHTML 流程页 html 内容。
func renderProcessesHTML(acts *action.Actions) string {
	var b strings.Builder
	b.WriteString(`<section id="processes"><h2>系统流程</h2><p class="muted">数据源：命令入口 + 索引调用链——每个命令跑起来调用什么。</p>`)
	for _, pe := range processEntries {
		chain := queryChain(acts, pe.Entry)
		b.WriteString(fmt.Sprintf("<h3>%s</h3>", htmlEsc(pe.Cmd)))
		if chain == nil || len(chain.Steps) == 0 {
			b.WriteString("<p class=\"muted\">（索引中无调用链）</p>")
			continue
		}
		b.WriteString("<p class=\"muted\">入口：" + htmlEsc(chain.Entry) + "</p>")
		b.WriteString("<pre class=\"mermaid\">" + htmlEsc(sequenceMermaid(chain.Steps)) + "</pre>")
		if len(chain.Pkgs) > 0 {
			b.WriteString("<p class=\"muted\">涉及包：" + htmlEsc(strings.Join(chain.Pkgs, "、")) + "</p>")
		}
	}
	b.WriteString("</section>")
	return b.String()
}
