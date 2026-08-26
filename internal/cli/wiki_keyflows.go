package cli

// R17 模块页关键数据流：核心符号的字段读写矩阵（value-trace 入口）——
// 新人看模块"处理什么数据"：读哪些字段、写哪些字段，可继续
// query trace-backward/forward 深挖。
// R92：数据查询迁 action（Actions.WikiKeyFlows——wiki 与 query
// processes 链同源）；本文件留模块装配与渲染。

import (
	"fmt"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
)

// wikiModuleKeyFlows 模块核心符号的关键数据流（R17）：CoreSymbols
// 批量查字段读写。用 canonical ID（名称跨包重名——FromContext 在
// logging/zap 等都有，ResolveSymbol 多匹配会失败）。
func wikiModuleKeyFlows(acts *action.Actions, wm *domain.WikiModule) []action.WikiKeyFlow {
	var ids []string
	for _, s := range wm.CoreSymbols {
		if s.ID != "" {
			ids = append(ids, s.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return acts.WikiKeyFlows(wm.Name, ids)
}

// renderKeyFlowsSectionMD 关键数据流区块（md）。
func renderKeyFlowsSectionMD(flows []action.WikiKeyFlow) string {
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
func renderKeyFlowsSectionHTML(flows []action.WikiKeyFlow) string {
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
