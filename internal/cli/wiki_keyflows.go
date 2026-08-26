package cli

// R17 模块页关键数据流：核心符号的字段读写矩阵（value-trace 入口）——
// 新人看模块"处理什么数据"：读哪些字段、写哪些字段，可继续
// query trace-backward/forward 深挖。

import (
	"fmt"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
)

// wikiKeyFlow 一个核心符号的字段读写摘要。
type wikiKeyFlow struct {
	Symbol string   `json:"symbol"`
	Reads  []string `json:"reads"`  // 字段路径（类型限定，去重）
	Writes []string `json:"writes"` // direct_write + indirect_write（去重）
}

// wikiKeyFlows 批量计算核心符号的字段读写数据流（R17）：每符号
// FunctionFields 分组——direct_read 归读、direct_write/indirect_write
// 归写。过滤：只保留本模块类型限定字段（排除第三方 x/tools/ssa 等
// 与 map 访问噪音）；[key] 变体归一后去重。无字段访问的符号跳过。
// R78：单符号解析失败跳过（不整批丢弃——流程页链上可能含外部库符号）。
func wikiKeyFlows(acts *action.Actions, modulePrefix string, symbolNames []string) []wikiKeyFlow {
	var out []wikiKeyFlow
	for _, name := range symbolNames {
		n, rows, err := acts.FunctionFields(name)
		if err != nil || len(rows) == 0 {
			continue
		}
		f := wikiKeyFlow{Symbol: n.Name}
		add := func(dst *[]string, path string) {
			// 只保留本模块类型限定字段（前缀 module；无包前缀的
			// map 访问 n["x"]/slots[key] 是噪音）
			if !strings.HasPrefix(path, modulePrefix) {
				return
			}
			// [key] 变体归一（fields[key] → fields）
			path = strings.ReplaceAll(path, "[key]", "")
			if !containsStr(*dst, path) {
				*dst = append(*dst, path)
			}
		}
		for _, r := range rows {
			switch r.AccessKind {
			case domain.SummaryDirectRead:
				add(&f.Reads, r.FieldPath)
			case domain.SummaryDirectWrite, domain.SummaryIndirectWrite:
				add(&f.Writes, r.FieldPath)
			}
		}
		if len(f.Reads) > 0 || len(f.Writes) > 0 {
			out = append(out, f)
		}
	}
	return out
}

// wikiModuleKeyFlows 模块核心符号的关键数据流（R17）：CoreSymbols
// 批量查字段读写。用 canonical ID（名称跨包重名——FromContext 在
// logging/zap 等都有，ResolveSymbol 多匹配会失败）。
func wikiModuleKeyFlows(acts *action.Actions, wm *domain.WikiModule) []wikiKeyFlow {
	var ids []string
	for _, s := range wm.CoreSymbols {
		if s.ID != "" {
			ids = append(ids, s.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return wikiKeyFlows(acts, wm.Name, ids)
}

// renderKeyFlowsSectionMD 关键数据流区块（md）。
func renderKeyFlowsSectionMD(flows []wikiKeyFlow) string {
	if len(flows) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## 关键数据流\n\n")
	b.WriteString("> 核心符号的字段读写（value-trace 入口：`query trace-backward/forward <字段>` 看产生与使用链）。\n\n")
	for _, f := range flows {
		b.WriteString("### " + f.Symbol + "\n\n")
		if len(f.Reads) > 0 {
			b.WriteString("- **读**：" + strings.Join(f.Reads, "、") + "\n")
		}
		if len(f.Writes) > 0 {
			b.WriteString("- **写**：" + strings.Join(f.Writes, "、") + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// renderKeyFlowsSectionHTML 关键数据流区块（html/serve 共用）。
func renderKeyFlowsSectionHTML(flows []wikiKeyFlow) string {
	if len(flows) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<section id="keyflows"><h2>关键数据流</h2><p class="muted">核心符号的字段读写（value-trace 入口：query trace-backward/forward &lt;字段&gt; 看产生与使用链）。</p>`)
	for _, f := range flows {
		b.WriteString(fmt.Sprintf("<h3>%s</h3>", htmlEsc(f.Symbol)))
		if len(f.Reads) > 0 {
			b.WriteString("<p><strong>读</strong>：" + htmlEsc(strings.Join(f.Reads, "、")) + "</p>")
		}
		if len(f.Writes) > 0 {
			b.WriteString("<p><strong>写</strong>：" + htmlEsc(strings.Join(f.Writes, "、")) + "</p>")
		}
	}
	b.WriteString("</section>")
	return b.String()
}
