package cli

// R50 流程调用链渲染公共函数（从 wiki_processes_routes.go 拆出——行数
// 治理）：renderProcChainMD/HTML（入口调用链图 + 涉及包；chain.Miss
// 优先——区分索引问题 vs 仅调用外部库）+ httpMissNote。

import (
	"strings"
)

// renderProcChainMD 入口调用链图 + 涉及包（md）。chain 空 → 说明文字
// （chain.Miss 优先——R50 区分索引问题 vs 仅调用外部库；miss 兜底）。
func renderProcChainMD(rc *wikiRenderCtx, chain *procChain, miss string) string {
	var b strings.Builder
	if chain == nil || len(chain.Steps) == 0 {
		if chain != nil && chain.Miss != "" {
			miss = chain.Miss
		}
		b.WriteString(miss + "\n\n")
		return b.String()
	}
	b.WriteString("入口：" + chain.Entry + "\n\n")
	eg, err := rc.acts.Entities()
	if err != nil {
		eg = nil
	}
	if sub := entitySubgraphMermaid(eg, chain.Steps); sub != "" {
		b.WriteString(rc.diagramMD(sub))
	} else {
		b.WriteString(rc.diagramMD(sequenceMermaid(chain.Steps)))
	}
	if seq := entitySequenceMermaid(eg, chain.Steps); seq != "" {
		b.WriteString("**实体间调用时序**（连续同向调用合并计数）：\n\n")
		b.WriteString(rc.diagramMD(seq))
	}
	if len(chain.KeyFlows) > 0 {
		// R78：关键数据流（链上符号字段读写——value-trace 深挖入口）
		b.WriteString("**关键数据流**（链上符号字段读写）：\n\n")
		for _, fl := range chain.KeyFlows {
			var parts []string
			if len(fl.Reads) > 0 {
				parts = append(parts, "读 "+strings.Join(fl.Reads, "、"))
			}
			if len(fl.Writes) > 0 {
				parts = append(parts, "写 "+strings.Join(fl.Writes, "、"))
			}
			b.WriteString("- `" + fl.Symbol + "`：" + strings.Join(parts, "；") + "\n")
		}
		b.WriteString("（`query trace-backward/forward <字段>` 深挖产生与使用链）\n\n")
	}
	if len(chain.Pkgs) > 0 {
		b.WriteString("涉及包：`" + strings.Join(chain.Pkgs, "`、`") + "`\n\n")
	}
	return b.String()
}

// renderProcChainHTML 入口调用链图 + 涉及包（html）。
func renderProcChainHTML(rc *wikiRenderCtx, chain *procChain, miss string) string {
	var b strings.Builder
	if chain == nil || len(chain.Steps) == 0 {
		if chain != nil && chain.Miss != "" {
			miss = chain.Miss
		}
		b.WriteString(`<p class="muted">` + miss + `</p>`)
		return b.String()
	}
	b.WriteString(`<p class="muted">入口：` + htmlEsc(chain.Entry) + `</p>`)
	eg, err := rc.acts.Entities()
	if err != nil {
		eg = nil
	}
	if sub := entitySubgraphMermaid(eg, chain.Steps); sub != "" {
		b.WriteString(rc.diagramHTML(sub))
	} else {
		b.WriteString(rc.diagramHTML(sequenceMermaid(chain.Steps)))
	}
	if seq := entitySequenceMermaid(eg, chain.Steps); seq != "" {
		b.WriteString("<p class=\"muted\">实体间调用时序（连续同向调用合并计数）：</p>")
		b.WriteString(rc.diagramHTML(seq))
	}
	if len(chain.KeyFlows) > 0 {
		// R78：关键数据流（链上符号字段读写——value-trace 深挖入口）
		b.WriteString("<p class=\"muted\"><strong>关键数据流</strong>（链上符号字段读写）：</p><ul>")
		for _, fl := range chain.KeyFlows {
			var parts []string
			if len(fl.Reads) > 0 {
				parts = append(parts, "读 "+strings.Join(fl.Reads, "、"))
			}
			if len(fl.Writes) > 0 {
				parts = append(parts, "写 "+strings.Join(fl.Writes, "、"))
			}
			b.WriteString("<li><code>" + htmlEsc(fl.Symbol) + "</code>：" + htmlEsc(strings.Join(parts, "；")) + "</li>")
		}
		b.WriteString(`</ul><p class="muted">query trace-backward/forward 深挖产生与使用链。</p>`)
	}
	if len(chain.Pkgs) > 0 {
		b.WriteString("<p class=\"muted\">涉及包：" + htmlEsc(strings.Join(chain.Pkgs, "、")) + "</p>")
	}
	return b.String()
}

// httpMissNote handler 无调用链的原因说明。
func httpMissNote(e httpProcEntry) string {
	switch {
	case e.Handler == "(匿名)":
		return "匿名 handler（不展开调用链）"
	case e.HandlerID == "" && strings.Contains(e.Handler, "."):
		return "方法值 handler 无 handler_id（需重建索引）"
	default:
		return "索引中无调用链——可能未重建索引"
	}
}
