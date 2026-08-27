package cli

// R37 gRPC 服务入口渲染（从 wiki_processes_routes.go 拆出——行数治理）：
// 服务索引（每服务独立子页链接，折叠清单带链接）+ 单服务子页（方法
// 入口逐个展开：调用链图 + 时序图 + 涉及包）。

import (
	"fmt"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
)

// grpcServiceList 流程页 gRPC 服务列表（索引节 + 子页写出共用；
// rc.acts nil——纯函数级测试——返回空）。repoAbs 用于 ServiceDesc
// 解析（方法全集 + handler）——空则 fallback 节点 methods 属性。
// R39：过滤 0 方法服务（自身 wiki 实测——Greeter 无实现无方法，
// 子页无内容）——不出索引项也不写子页。
func grpcServiceList(rc *wikiRenderCtx) []action.GrpcRouteService {
	if rc.acts == nil {
		return nil
	}
	svcs, err := rc.acts.GrpcRoutes(rc.RepoAbs)
	if err != nil {
		return nil
	}
	var out []action.GrpcRouteService
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
//  1. yaml domains.services 服务名精确匹配（AI 归纳 + 人工确认——权威）
//  2. 否则方法调用链涉及包匹配 domains.packages 投票（多数派；平局取
//     首个——OrderService → order（交易履约域）实测）
//  3. 无匹配 → ""（渲染为「其他」目录）
//
// R78：投票排除服务实现包（svc.ImplID 的包——go2o 实测 AI 把
// impl/domain、impl/service 实现包归入基础设施兜底域，不排除时投票
// 被污染（服务实现包是调用链必然命中项，兜底域 60+ 包轻松得票））。
func serviceDomain(rc *wikiRenderCtx, svc action.GrpcRouteService) string {
	for _, d := range rc.cfg.Domains {
		for _, s := range d.Services {
			if s == svc.Name {
				return d.Name
			}
		}
	}
	// 实现包（服务方法所在包——归属信号被 AI 归入兜底域，投票排除）
	implPkg := ""
	if svc.ImplID != "" {
		implPkg = action.SymbolPkg(svc.ImplID)
	}
	votes := map[string]int{}
	var order []string
	for _, p := range rc.acts.GrpcProcMethods(svc) {
		if p.Chain == nil {
			continue
		}
		for _, pk := range p.Chain.Pkgs {
			if implPkg != "" && pk == implPkg {
				continue
			}
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
	if best == "" {
		// R100 待办14-②：静态兜底——服务名前缀 → 表前缀域匹配
		// （ItemService → item → domains[].tables 前缀命中域）
		return serviceStaticDomain(rc, svc)
	}
	return best
}

// serviceStaticDomain 服务归属静态兜底（R100 待办14-②）：投票无命中
// 时，服务名去 Service 后缀转小写 → 表前缀域匹配（ItemService → item
// → domains.tables 前缀命中域）；无匹配返回空（走「其他」）。
func serviceStaticDomain(rc *wikiRenderCtx, svc action.GrpcRouteService) string {
	prefix := strings.ToLower(strings.TrimSuffix(svc.Name, "Service"))
	if prefix == "" {
		return ""
	}
	for _, d := range rc.cfg.Domains {
		for _, t := range d.Tables {
			if strings.HasPrefix(t, prefix) {
				return d.Name
			}
		}
	}
	return ""
}

// grpcDomainGroup 领域分组（R38 目录化：服务子页按领域分目录）。
type grpcDomainGroup struct {
	Name     string
	Services []action.GrpcRouteService
}

// grpcServicesByDomain 服务按领域分组（确定性：领域名排序；「其他」最后）。
func grpcServicesByDomain(rc *wikiRenderCtx, services []action.GrpcRouteService) []grpcDomainGroup {
	byName := map[string][]action.GrpcRouteService{}
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
func renderGrpcIndexMD(rc *wikiRenderCtx, services []action.GrpcRouteService, max int) string {
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
func renderGrpcIndexHTML(rc *wikiRenderCtx, services []action.GrpcRouteService, max int) string {
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
