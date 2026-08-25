package cli

// R37 gRPC 服务入口渲染（从 wiki_processes_routes.go 拆出——行数治理）：
// 服务索引（每服务独立子页链接，折叠清单带链接）+ 单服务子页（方法
// 入口逐个展开：调用链图 + 时序图 + 涉及包）。

import (
	"fmt"
	"sort"
	"strings"
)

// grpcServiceList 流程页 gRPC 服务列表（索引节 + 子页写出共用；
// rc.repo nil——纯函数级测试——返回空）。repoAbs 用于 ServiceDesc
// 解析（方法全集 + handler）——空则 fallback 节点 methods 属性。
// R39：过滤 0 方法服务（自身 wiki 实测——Greeter 无实现无方法，
// 子页无内容）——不出索引项也不写子页。
func grpcServiceList(rc *wikiRenderCtx) []grpcRouteService {
	if rc.repo == nil {
		return nil
	}
	svcs, err := grpcRoutes(rc.repo, rc.RepoAbs)
	if err != nil {
		return nil
	}
	var out []grpcRouteService
	for _, s := range svcs.Services {
		if len(s.Methods) > 0 {
			out = append(out, s)
		}
	}
	return out
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

// svcOtherDomain 无领域归属服务的统一目录名（R38 用户定案）。
const svcOtherDomain = "其他"

// serviceDomain 服务归属领域（R38 用户定案：yaml 显式 + 调用链投票兜底）：
// 1. yaml domains.services 服务名精确匹配（AI 归纳 + 人工确认——权威）
// 2. 否则方法调用链涉及包匹配 domains.packages 投票（多数派；平局取
//    首个——OrderService → order（交易履约域）实测）
// 3. 无匹配 → ""（渲染为「其他」目录）
func serviceDomain(rc *wikiRenderCtx, svc grpcRouteService) string {
	for _, d := range rc.cfg.Domains {
		for _, s := range d.Services {
			if s == svc.Name {
				return d.Name
			}
		}
	}
	votes := map[string]int{}
	var order []string
	for _, p := range grpcProcMethods(rc.acts, svc) {
		if p.Chain == nil {
			continue
		}
		for _, pk := range p.Chain.Pkgs {
			for _, d := range rc.cfg.Domains {
				for _, dp := range d.Packages {
					if dp != "" && (strings.HasSuffix(pk, "/"+dp) || pk == dp) {
						if votes[d.Name] == 0 {
							order = append(order, d.Name)
						}
						votes[d.Name]++
						break
					}
				}
			}
		}
	}
	best, bestN := "", 0
	for _, dn := range order {
		if votes[dn] > bestN {
			best, bestN = dn, votes[dn]
		}
	}
	return best
}

// grpcDomainGroup 领域分组（R38 目录化：服务子页按领域分目录）。
type grpcDomainGroup struct {
	Name     string
	Services []grpcRouteService
}

// grpcServicesByDomain 服务按领域分组（确定性：领域名排序；「其他」最后）。
func grpcServicesByDomain(rc *wikiRenderCtx, services []grpcRouteService) []grpcDomainGroup {
	byName := map[string][]grpcRouteService{}
	var names []string
	for _, s := range services {
		d := serviceDomain(rc, s)
		if d == "" {
			d = svcOtherDomain
		}
		if _, ok := byName[d]; !ok {
			names = append(names, d)
		}
		byName[d] = append(byName[d], s)
	}
	var groups []grpcDomainGroup
	for _, n := range names {
		if n != svcOtherDomain {
			groups = append(groups, grpcDomainGroup{Name: n, Services: byName[n]})
		}
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
	if o, ok := byName[svcOtherDomain]; ok {
		groups = append(groups, grpcDomainGroup{Name: svcOtherDomain, Services: o})
	}
	return groups
}

// grpcSvcPagePath 服务子页路径（相对 wiki 根：<domain>/processes-grpc-<svc>.md
// ——R38 按领域分目录；ext 为 .md/.html）。
func grpcSvcPagePath(domain, svc, ext string) string {
	return domain + "/processes-grpc-" + grpcSvcFileName(svc) + ext
}

// renderGrpcIndexMD gRPC 服务入口索引（md：按领域分组——每组服务子页
// 链接带目录路径；组内超上限折叠）。
func renderGrpcIndexMD(rc *wikiRenderCtx, services []grpcRouteService, max int) string {
	if len(services) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## gRPC 服务入口\n\n> 数据源：grpc_service 节点 + grpc_impl 边——每个服务方法一个入口（(Impl).Method 调用链），每服务独立子页（按业务领域分目录）。\n\n")
	for _, g := range grpcServicesByDomain(rc, services) {
		b.WriteString("### 领域 " + g.Name + "\n\n")
		expanded, folded := procFold(max, g.Services)
		for _, s := range expanded {
			impl := s.Impl
			if impl == "" {
				impl = "（未识别实现）"
			}
			b.WriteString(fmt.Sprintf("- [%s](%s)——实现 %s，%d 个方法\n",
				s.Name, grpcSvcPagePath(g.Name, s.Name, ".md"), impl, len(s.Methods)))
		}
		if len(folded) > 0 {
			b.WriteString(fmt.Sprintf("其余 %d 个服务仅列清单（子页仍可用）：\n\n", len(folded)))
			for _, s := range folded {
				b.WriteString(fmt.Sprintf("- [%s](%s)——%d 个方法（%s）\n",
					s.Name, grpcSvcPagePath(g.Name, s.Name, ".md"), len(s.Methods), s.Register))
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

// renderGrpcIndexHTML gRPC 服务入口节（html 单文件版——R40 用户要求：
// 服务子页内容内嵌进 index.html，所有东西都在一个文件里；按领域分组，
// 服务用 <details> 折叠（summary = 服务名 + 实现 + 方法数），展开看
// 方法级调用链）。
func renderGrpcIndexHTML(rc *wikiRenderCtx, services []grpcRouteService, max int) string {
	if len(services) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<h2>gRPC 服务入口</h2><p class="muted">数据源：grpc_service 节点 + grpc_impl 边——每个服务方法一个入口（(Impl).Method 调用链），方法级展开内嵌（单文件）。</p>`)
	for _, g := range grpcServicesByDomain(rc, services) {
		b.WriteString(fmt.Sprintf(`<h3>领域 %s</h3>`, htmlEsc(g.Name)))
		expanded, folded := procFold(max, g.Services)
		for _, s := range expanded {
			b.WriteString(renderGrpcServiceHTML(rc, s, procMaxOf(rc.MaxEntries)))
		}
		if len(folded) > 0 {
			b.WriteString(fmt.Sprintf(`<details><summary>其余 %d 个服务（全部内嵌，展开查看）</summary>`, len(folded)))
			for _, s := range folded {
				b.WriteString(renderGrpcServiceHTML(rc, s, procMaxOf(rc.MaxEntries)))
			}
			b.WriteString("</details>")
		}
	}
	return b.String()
}

// renderGrpcServiceMD 单个 gRPC 服务子页（md）：方法入口逐个展开。
// R62：顶部加全部方法表格（方法/handler/调用链状态——图太严格
// （无调用链）时表格兜底，至少能看到完整方法清单）。
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
	b.WriteString("### 全部方法\n\n")
	b.WriteString("| 方法 | handler | 调用链 |\n|---|---|---|\n")
	for _, p := range methods {
		b.WriteString(fmt.Sprintf("| %s | %s | %s |\n", p.Name, p.Handler, grpcMethodStatus(p)))
	}
	b.WriteString("\n")
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

// grpcMethodStatus 方法调用链状态（表格列：有图/无调用链原因）。
func grpcMethodStatus(p grpcMethodProc) string {
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
func renderGrpcServiceHTML(rc *wikiRenderCtx, svc grpcRouteService, max int) string {
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
	methods := grpcProcMethods(rc.acts, svc)
	// R62：全部方法表格（图太严格时表格兜底——完整方法清单可见）
	b.WriteString(`<table><thead><tr><th>方法</th><th>handler</th><th>调用链</th></tr></thead><tbody>`)
	for _, p := range methods {
		b.WriteString("<tr><td><code>" + htmlEsc(p.Name) + "</code></td><td><code>" + htmlEsc(p.Handler) + "</code></td><td>" + htmlEsc(grpcMethodStatus(p)) + "</td></tr>")
	}
	b.WriteString("</tbody></table>")
	expanded, folded := procFold(max, methods)
	for _, p := range expanded {
		b.WriteString("<h4>" + htmlEsc(p.Name) + "</h4>")
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
	b.WriteString("</details>")
	return b.String()
}
