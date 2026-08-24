package cli

// R37 gRPC 服务入口渲染（从 wiki_processes_routes.go 拆出——行数治理）：
// 服务索引（每服务独立子页链接，折叠清单带链接）+ 单服务子页（方法
// 入口逐个展开：调用链图 + 时序图 + 涉及包）。

import (
	"fmt"
	"strings"
)

// grpcServiceList 流程页 gRPC 服务列表（索引节 + 子页写出共用；
// rc.repo nil——纯函数级测试——返回空）。repoAbs 用于 ServiceDesc
// 解析（方法全集 + handler）——空则 fallback 节点 methods 属性。
func grpcServiceList(rc *wikiRenderCtx) []grpcRouteService {
	if rc.repo == nil {
		return nil
	}
	svcs, err := grpcRoutes(rc.repo, rc.RepoAbs)
	if err != nil {
		return nil
	}
	return svcs.Services
}

// grpcSvcFileName 服务子页文件名（md/html 共用基础名；服务名做文件名清洗）。
func grpcSvcFileName(svc string) string {
	var b strings.Builder
	for _, r := range svc {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "service"
	}
	return b.String()
}

// renderGrpcIndexMD gRPC 服务入口索引（md：每服务一子页链接）。
func renderGrpcIndexMD(rc *wikiRenderCtx, services []grpcRouteService, max int) string {
	if len(services) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## gRPC 服务入口\n\n> 数据源：grpc_service 节点 + grpc_impl 边——每个服务方法一个入口（(Impl).Method 调用链），每服务独立子页。\n\n")
	expanded, folded := procFold(max, services)
	for _, s := range expanded {
		impl := s.Impl
		if impl == "" {
			impl = "（未识别实现）"
		}
		b.WriteString(fmt.Sprintf("- [%s](processes-grpc-%s.md)——实现 %s，%d 个方法\n",
			s.Name, grpcSvcFileName(s.Name), impl, len(s.Methods)))
	}
	if len(folded) > 0 {
		b.WriteString(fmt.Sprintf("其余 %d 个服务仅列清单（子页仍可用）：\n\n", len(folded)))
		for _, s := range folded {
			b.WriteString(fmt.Sprintf("- [%s](processes-grpc-%s.md)——%d 个方法（%s）\n",
				s.Name, grpcSvcFileName(s.Name), len(s.Methods), s.Register))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// renderGrpcIndexHTML gRPC 服务入口索引（html）。
func renderGrpcIndexHTML(rc *wikiRenderCtx, services []grpcRouteService, max int) string {
	if len(services) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<h2>gRPC 服务入口</h2><p class="muted">数据源：grpc_service 节点 + grpc_impl 边——每个服务方法一个入口（(Impl).Method 调用链），每服务独立子页。</p><ul>`)
	expanded, folded := procFold(max, services)
	for _, s := range expanded {
		impl := s.Impl
		if impl == "" {
			impl = "（未识别实现）"
		}
		b.WriteString(fmt.Sprintf(`<li><a href="processes-grpc-%s.html">%s</a>——实现 %s，%d 个方法</li>`,
			grpcSvcFileName(s.Name), htmlEsc(s.Name), htmlEsc(impl), len(s.Methods)))
	}
	if len(folded) > 0 {
		b.WriteString(fmt.Sprintf(`<li><details><summary>其余 %d 个服务仅列清单（子页仍可用）</summary><ul>`, len(folded)))
		for _, s := range folded {
			b.WriteString(fmt.Sprintf(`<li><a href="processes-grpc-%s.html">%s</a>——%d 个方法（%s）</li>`,
				grpcSvcFileName(s.Name), htmlEsc(s.Name), len(s.Methods), htmlEsc(s.Register)))
		}
		b.WriteString("</ul></details></li>")
	}
	b.WriteString("</ul>")
	return b.String()
}

// renderGrpcServiceMD 单个 gRPC 服务子页（md）：方法入口逐个展开。
func renderGrpcServiceMD(rc *wikiRenderCtx, svc grpcRouteService, max int) string {
	var b strings.Builder
	b.WriteString("# gRPC 服务流程：" + svc.Name + "\n\n")
	if svc.Impl != "" {
		b.WriteString("实现：" + svc.Impl)
		if svc.ImplFile != "" {
			b.WriteString("（" + svc.ImplFile + "）")
		}
		b.WriteString("；注册：" + svc.Register + "\n\n")
	}
	methods := grpcProcMethods(rc.acts, svc)
	expanded, folded := procFold(max, methods)
	for _, p := range expanded {
		b.WriteString("## " + p.Name + "\n\n")
		if p.Handler != "" {
			b.WriteString("handler：" + p.Handler + "\n\n")
		}
		b.WriteString(renderProcChainMD(rc, p.Chain, grpcMethodMissNote(p)))
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

// renderGrpcServiceHTML 单个 gRPC 服务子页 html 内容（嵌入 wikiHTMLPage）。
func renderGrpcServiceHTML(rc *wikiRenderCtx, svc grpcRouteService, max int) string {
	var b strings.Builder
	b.WriteString(`<section><h2>gRPC 服务流程：` + htmlEsc(svc.Name) + `</h2>`)
	if svc.Impl != "" {
		impl := htmlEsc(svc.Impl)
		if svc.ImplFile != "" {
			impl += "（" + htmlEsc(svc.ImplFile) + "）"
		}
		b.WriteString(`<p class="muted">实现：` + impl + `；注册：` + htmlEsc(svc.Register) + `</p>`)
	}
	methods := grpcProcMethods(rc.acts, svc)
	expanded, folded := procFold(max, methods)
	for _, p := range expanded {
		b.WriteString("<h3>" + htmlEsc(p.Name) + "</h3>")
		if p.Handler != "" {
			b.WriteString(`<p class="muted">handler：` + htmlEsc(p.Handler) + `</p>`)
		}
		b.WriteString(renderProcChainHTML(rc, p.Chain, grpcMethodMissNote(p)))
	}
	if len(folded) > 0 {
		b.WriteString(fmt.Sprintf(`<details><summary>其余 %d 个方法仅列清单（--max-entries 可调上限）</summary><ul>`, len(folded)))
		for _, p := range folded {
			b.WriteString("<li>" + htmlEsc(p.Name) + "</li>")
		}
		b.WriteString("</ul></details>")
	}
	b.WriteString("</section>")
	return b.String()
}
