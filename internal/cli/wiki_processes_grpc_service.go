package cli

// R37 单个 gRPC 服务子页渲染（R92 从 wiki_processes_grpc.go 拆出——
// 行数治理）：方法入口逐个展开（调用链图 + 时序图 + 涉及包）+ 方法
// 表格兜底。

import (
	"fmt"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
)

// renderGrpcServiceMD 单个 gRPC 服务子页（md）：方法入口逐个展开。
// R62：顶部加全部方法表格（方法/handler/调用链状态——图太严格
// （无调用链）时表格兜底，至少能看到完整方法清单）。
func renderGrpcServiceMD(rc *wikiRenderCtx, svc action.GrpcRouteService, max int) string {
	var b strings.Builder
	b.WriteString("# gRPC 服务流程：" + svc.Name + "\n\n")
	if svc.Impl != "" {
		b.WriteString("实现：" + svc.Impl)
		if svc.ImplFile != "" {
			b.WriteString("（" + svc.ImplFile + "）")
		}
		b.WriteString("；注册：" + svc.Register + "\n\n")
	}
	methods := rc.acts.GrpcProcMethods(svc)
	b.WriteString("### 全部方法\n\n")
	b.WriteString("| 方法 | handler | 调用链 |\n|---|---|---|\n")
	for _, p := range methods {
		b.WriteString(fmt.Sprintf("| %s | %s | %s |\n", p.Name, p.Handler, grpcMethodStatus(p)))
	}
	b.WriteString("\n")
	expanded, folded := procFold(max, methods)
	// W2：具体方法折叠（details——展开才看调用链/时序图）
	for _, p := range expanded {
		summary := "方法 " + p.Name
		if p.Handler != "" {
			summary += "（handler " + p.Handler + "）"
		}
		b.WriteString("<details><summary>" + summary + "</summary>\n\n")
		// R83：代码级时序（源码 AST，depth=rc.SeqDepth）优先；fallback 索引链
		entryID := ""
		if svc.ImplID != "" {
			entryID = action.GrpcMethodEntryID(svc.ImplID, p.Name)
		}
		b.WriteString(renderProcSeqMD(rc, entryID, p.Chain, grpcMethodMissNote(p)))
		b.WriteString("</details>\n\n")
	}
	if len(folded) > 0 {
		b.WriteString(fmt.Sprintf("其余 %d 个方法仅列清单（--max-entries 可调上限）：\n\n", len(folded)))
		for _, p := range folded {
			b.WriteString("- " + p.Name + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// grpcMethodStatus 方法调用链状态（表格列：有图/无调用链原因）。
func grpcMethodStatus(p action.GrpcMethodProc) string {
	if p.Chain != nil && len(p.Chain.Steps) > 0 {
		return "有调用链（见下）"
	}
	if p.Chain != nil && p.Chain.Miss != "" {
		return p.Chain.Miss
	}
	return grpcMethodMissNote(p)
}

// renderGrpcServiceHTML 单个 gRPC 服务流程内容（R40：内嵌进 index.html
// 单文件——<details> 折叠；summary = 服务名 + 实现 + 方法数）。
func renderGrpcServiceHTML(rc *wikiRenderCtx, svc action.GrpcRouteService, max int) string {
	var b strings.Builder
	summary := "服务 " + htmlEsc(svc.Name)
	if svc.Impl != "" {
		summary += "——实现 " + htmlEsc(svc.Impl)
	}
	summary += fmt.Sprintf("，%d 个方法", len(svc.Methods))
	b.WriteString(`<details><summary>` + summary + `</summary>`)
	if svc.ImplFile != "" {
		b.WriteString(`<p class="muted">实现位置：` + htmlEsc(svc.ImplFile) + `；注册：` + htmlEsc(svc.Register) + `</p>`)
	}
	methods := rc.acts.GrpcProcMethods(svc)
	// R62：全部方法表格（图太严格时表格兜底——完整方法清单可见）
	b.WriteString(`<table><thead><tr><th>方法</th><th>handler</th><th>调用链</th></tr></thead><tbody>`)
	for _, p := range methods {
		b.WriteString("<tr><td><code>" + htmlEsc(p.Name) + "</code></td><td><code>" + htmlEsc(p.Handler) + "</code></td><td>" + htmlEsc(grpcMethodStatus(p)) + "</td></tr>")
	}
	b.WriteString("</tbody></table>")
	expanded, folded := procFold(max, methods)
	// W2：具体方法折叠（summary = 方法名 + handler——展开才看调用链/
	// 时序图；服务名折叠 → 方法折叠两层）
	for _, p := range expanded {
		summary := "方法 " + htmlEsc(p.Name)
		if p.Handler != "" {
			summary += "——handler " + htmlEsc(p.Handler)
		}
		b.WriteString(`<details><summary>` + summary + `</summary>`)
		// R83：代码级时序优先（fallback 索引链）
		entryID := ""
		if svc.ImplID != "" {
			entryID = action.GrpcMethodEntryID(svc.ImplID, p.Name)
		}
		b.WriteString(renderProcSeqHTML(rc, entryID, p.Chain, grpcMethodMissNote(p)))
		b.WriteString("</details>")
	}
	if len(folded) > 0 {
		b.WriteString(fmt.Sprintf(`<details><summary>其余 %d 个方法仅列清单（--max-entries 可调上限）</summary><ul>`, len(folded)))
		for _, p := range folded {
			b.WriteString("<li>" + htmlEsc(p.Name) + "</li>")
		}
		b.WriteString("</ul></details>")
	}
	b.WriteString("</details>")
	return b.String()
}
