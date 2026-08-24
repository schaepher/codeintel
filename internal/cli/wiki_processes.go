package cli

// R2 系统级流程页（进程视角）：目标仓库 main 入口 → 一级调用函数逐条
// 展开深度 2 调用链（索引 callees 自动）→ 涉及包——新人看「入口跑
// 起来发生了什么」。不再硬编码 codeintel 自身命令（F1 遗留——go2o
// 验收暴露：12 个 codeintel 命令在目标索引全无调用链，wiki-check
// 时序 FAIL）。数据源均为代码事实，不依赖 AI。

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
)

// procChain 一条流程的调用链（入口 + 边 + 涉及包）。
type procChain struct {
	Entry string // 入口符号名
	Steps []domain.WikiSeqStep
	Pkgs  []string
}

// queryChain 查询入口符号的深度 2 调用链 + 涉及包（短名展示）。
// R13：steps 按源码调用行号排序（sortChainByCallLine）——顺序与
// 实际代码一一对应（此前 SQL 遍历序与源码序不一致）。
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
	chain.Steps = sortChainByCallLine(string(entry.ID), facts)
	pkgs := map[string]bool{}
	pkgs[symbolPkg(string(entry.ID))] = true
	for _, f := range facts {
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

// renderProcessesMD 流程页 Markdown：目标仓库 main 入口 + 一级调用
// 函数逐条展开深度 2 调用链（实体协作子图 + 涉及包）。R9：函数级
// 时序图替换为实体协作子图（Q7）——调用链映射到实体（类型/包门面）；
// 函数级细节可用 query callees 查。
func renderProcessesMD(rc *wikiRenderCtx) string {
	acts := rc.acts
	var b strings.Builder
	b.WriteString("# 系统流程\n\n> 数据源：目标仓库 main 入口函数 + 一级调用展开（索引调用链\n> GetCallees 深度 2）——实体协作视角看入口涉及的对象交互。\n\n")
	eg, err := acts.Entities()
	if err != nil {
		eg = nil
	}
	entries := entrySymbols(acts)
	if len(entries) == 0 {
		b.WriteString("未找到 main 入口（库项目或入口不在索引中）。\n")
		return b.String()
	}
	for _, e := range entries {
		b.WriteString("## 入口 `" + e.Name + "`\n\n")
		if e.File != "" {
			b.WriteString("位置: " + e.File)
			if e.Line > 0 {
				b.WriteString(":" + strconv.Itoa(e.Line))
			}
			b.WriteString("\n\n")
		}
		if len(e.Callees) == 0 {
			b.WriteString("一级调用: （无）\n\n")
			continue
		}
		for i, c := range e.Callees {
			b.WriteString("### " + c + "\n\n")
			// 完整 canonical ID 展开（短名 pkg:name 无法按名解析——
			// go2o 实测 app:ParseFlags 解析失败致无调用链）
			target := ""
			if i < len(e.CalleeIDs) {
				target = e.CalleeIDs[i]
			}
			chain := queryChain(acts, target)
			if chain == nil || len(chain.Steps) == 0 {
				b.WriteString("（索引中无调用链——可能未重建索引）\n\n")
				continue
			}
			b.WriteString("入口：" + chain.Entry + "\n\n")
			if sub := entitySubgraphMermaid(eg, chain.Steps); sub != "" {
				b.WriteString(rc.diagramMD(sub))
			} else {
				b.WriteString(rc.diagramMD(sequenceMermaid(chain.Steps)))
			}
			// R12：实体间调用时序图（顺序视角——谁先调谁）
			if seq := entitySequenceMermaid(eg, chain.Steps); seq != "" {
				b.WriteString("**实体间调用时序**（连续同向调用合并计数）：\n\n")
				b.WriteString(rc.diagramMD(seq))
			}
			if len(chain.Pkgs) > 0 {
				b.WriteString("涉及包：`" + strings.Join(chain.Pkgs, "`、`") + "`\n\n")
			}
		}
	}
	// R37：HTTP 路由入口（handler 调用链，同 handler 去重）——数据源
	// http_route 节点（构建期识别）；rc.repo nil（纯函数级测试）跳过
	if rc.repo != nil {
		if h := renderHTTPRoutesMD(rc, httpProcEntries(rc.acts, rc.repo), procMaxOf(rc.MaxEntries)); h != "" {
			b.WriteString(h)
		}
		// gRPC 服务入口索引（每服务独立子页——子页文件由渲染器写出）
		if svcs := grpcServiceList(rc); len(svcs) > 0 {
			if g := renderGrpcIndexMD(rc, svcs, procMaxOf(rc.MaxEntries)); g != "" {
				b.WriteString(g)
			}
		}
	}
	return b.String()
}

// renderProcessesHTML 流程页 html 内容（实体协作子图版）。
func renderProcessesHTML(rc *wikiRenderCtx) string {
	acts := rc.acts
	var b strings.Builder
	b.WriteString(`<section id="processes"><h2>系统流程</h2><p class="muted">数据源：目标仓库 main 入口 + 一级调用展开（索引调用链）——实体协作视角看入口涉及的对象交互。</p>`)
	eg, err := acts.Entities()
	if err != nil {
		eg = nil
	}
	entries := entrySymbols(acts)
	if len(entries) == 0 {
		b.WriteString(`<p>未找到 main 入口（库项目或入口不在索引中）。</p></section>`)
		return b.String()
	}
	for _, e := range entries {
		b.WriteString(fmt.Sprintf(`<h3>入口 <code>%s</code></h3>`, htmlEsc(e.Name)))
		if e.File != "" {
			loc := htmlEsc(e.File)
			if e.Line > 0 {
				loc += ":" + strconv.Itoa(e.Line)
			}
			b.WriteString(`<p class="muted">` + loc + `</p>`)
		}
		if len(e.Callees) == 0 {
			b.WriteString(`<p class="muted">一级调用：（无）</p>`)
			continue
		}
		for i, c := range e.Callees {
			b.WriteString(fmt.Sprintf(`<h4><code>%s</code></h4>`, htmlEsc(c)))
			target := ""
			if i < len(e.CalleeIDs) {
				target = e.CalleeIDs[i]
			}
			chain := queryChain(acts, target)
			if chain == nil || len(chain.Steps) == 0 {
				b.WriteString("<p class=\"muted\">（索引中无调用链）</p>")
				continue
			}
			b.WriteString("<p class=\"muted\">入口：" + htmlEsc(chain.Entry) + "</p>")
			if sub := entitySubgraphMermaid(eg, chain.Steps); sub != "" {
				b.WriteString(rc.diagramHTML(sub))
			} else {
				b.WriteString(rc.diagramHTML(sequenceMermaid(chain.Steps)))
			}
			// R12：实体间调用时序图
			if seq := entitySequenceMermaid(eg, chain.Steps); seq != "" {
				b.WriteString("<p class=\"muted\">实体间调用时序（连续同向调用合并计数）：</p>")
				b.WriteString(rc.diagramHTML(seq))
			}
			if len(chain.Pkgs) > 0 {
				b.WriteString("<p class=\"muted\">涉及包：" + htmlEsc(strings.Join(chain.Pkgs, "、")) + "</p>")
			}
		}
	}
	// R37：HTTP 路由入口 + gRPC 服务入口索引（每服务独立子页）；
	// rc.repo nil（纯函数级测试）跳过
	if rc.repo != nil {
		if h := renderHTTPRoutesHTML(rc, httpProcEntries(rc.acts, rc.repo), procMaxOf(rc.MaxEntries)); h != "" {
			b.WriteString(h)
		}
		if svcs := grpcServiceList(rc); len(svcs) > 0 {
			if g := renderGrpcIndexHTML(rc, svcs, procMaxOf(rc.MaxEntries)); g != "" {
				b.WriteString(g)
			}
		}
	}
	b.WriteString("</section>")
	return b.String()
}
