package cli

// R37 流程页路由入口（待办 4 完成：系统流程基于 http/grpc 入口）：
// - HTTP 路由入口：http_route 节点 → handler_id（发射端 canonical ID）
//   展开调用链，同 handler 多路由去重，resolver 分组（native/gin）；
//   老索引无 handler_id 时按函数短名 fallback 解析（方法值无法解析）
// - gRPC 服务方法入口：grpc_impl 实现类型 ID + 方法名构造 canonical
//   ID（(Impl).Method）展开；每服务独立子页（md/html），页内上限折叠
// - 规模控制（用户定案）：每节/每页展开上限 procMaxEntries（--max-entries
//   可调），超出部分折叠为清单
// 拆分：gRPC 渲染部分在 wiki_processes_grpc.go（行数治理）
// R92：查询/数据函数迁 action（Actions.HTTPRoutes/QueryChain、
// GrpcProcMethods/GrpcMethodEntryID/GrpcHandlerGoMethod）；本文件留
// httpProcEntries 装配与渲染。

import (
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
)

// procMaxEntries 每节/每页入口展开上限（R37：超出折叠为清单）。
const procMaxEntries = 15

// procMaxOf 上限取值（0 = 默认——--max-entries 未传时）。
func procMaxOf(max int) int {
	if max <= 0 {
		return procMaxEntries
	}
	return max
}

// httpProcEntry 一个 HTTP 流程入口（同 handler 去重后；匿名/无 id 每路由一个）。
type httpProcEntry struct {
	Handler   string // handler 名（展示；匿名 = "(匿名)"）
	HandlerID string // canonical ID（展开用；可能为空）
	Paths     []string // 该 handler 注册的路由（"GET /ping" 形态）
	Resolver  string
	Register  string // 首个注册点
	Chain     *action.ProcChain
}

// httpProcEntries 读 http_route 节点 → 按 handler_id 去重 → 展开调用链。
// handler_id 空：匿名/方法值（s.orders 无法 fallback）→ Chain nil 仅列路由；
// 具名函数短名 fallback ResolveSymbol（老索引兼容，多匹配失败为 nil）。
func httpProcEntries(acts *action.Actions) []httpProcEntry {
	res, err := acts.HTTPRoutes()
	if err != nil || len(res.Routes) == 0 {
		return nil
	}
	var out []httpProcEntry
	// 无 handler_id 的短名 fallback 缓存（多路由同短名只解析一次）
	shortResolved := map[string]*action.ProcChain{}
	for _, r := range res.Routes {
		if r.HandlerID != "" {
			if i := indexOfProc(out, r.HandlerID); i >= 0 {
				// 多模块仓库同一源码重复加载会重复发射（ana 8 个 go.mod
				// 实测）——同 path 去重
				if lab := routeLabel(r); !containsStr(out[i].Paths, lab) {
					out[i].Paths = append(out[i].Paths, lab)
				}
				continue
			}
			chain := acts.QueryChain(r.HandlerID)
			out = append(out, httpProcEntry{
				Handler: r.Handler, HandlerID: r.HandlerID,
				Paths: []string{routeLabel(r)}, Resolver: r.Resolver,
				Register: r.Register, Chain: chain,
			})
			continue
		}
		// 无 handler_id：方法值/匿名不可解析；具名短名 fallback
		var chain *action.ProcChain
		if !strings.Contains(r.Handler, ".") && r.Handler != "" && r.Handler != "(匿名)" {
			if c, ok := shortResolved[r.Handler]; ok {
				chain = c
			} else {
				chain = acts.QueryChain(r.Handler)
				shortResolved[r.Handler] = chain
			}
		}
		out = append(out, httpProcEntry{
			Handler: r.Handler, HandlerID: "",
			Paths: []string{routeLabel(r)}, Resolver: r.Resolver,
			Register: r.Register, Chain: chain,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Resolver != out[j].Resolver {
			return out[i].Resolver < out[j].Resolver
		}
		return out[i].Handler < out[j].Handler
	})
	return out
}

// indexOfProc 找同 handler_id 的入口（去重合并）。
func indexOfProc(xs []httpProcEntry, handlerID string) int {
	for i := range xs {
		if xs[i].HandlerID == handlerID {
			return i
		}
	}
	return -1
}

// routeLabel 路由展示标签（method 空 = ANY）。
func routeLabel(r action.HTTPRouteEntry) string {
	if r.Method == "" {
		return "ANY " + r.Path
	}
	return r.Method + " " + r.Path
}

// procFold 入口超限拆分：前 max 完整展开，其余折叠（渲染时清单）。
func procFold[T any](max int, xs []T) (expanded []T, folded []T) {
	if max <= 0 || len(xs) <= max {
		return xs, nil
	}
	return xs[:max], xs[max:]
}

// renderProcChainMD / renderProcChainHTML / httpMissNote 已拆到
// wiki_processes_chain.go（行数治理）。

// renderHTTPRoutesMD HTTP 路由入口节（md）。
func renderHTTPRoutesMD(rc *wikiRenderCtx, entries []httpProcEntry, max int) string {
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## HTTP 路由入口\n\n> 数据源：http_route 节点（构建期识别）→ handler 调用链展开（同 handler 多路由去重）。\n\n")
	expanded, folded := procFold(max, entries)
	curResolver := ""
	for _, e := range expanded {
		if e.Resolver != curResolver {
			curResolver = e.Resolver
			b.WriteString("### [" + curResolver + "]\n\n")
		}
		b.WriteString("#### `" + strings.Join(e.Paths, "`、`") + "`\n\n")
		if e.Register != "" {
			b.WriteString("注册点：" + e.Register + "\n\n")
		}
		b.WriteString(renderProcChainMD(rc, e.Chain, httpMissNote(e)))
	}
	if len(folded) > 0 {
		b.WriteString("其余 " + itoa(len(folded)) + " 个入口仅列清单（--max-entries 可调上限）：\n\n")
		for _, e := range folded {
			loc := e.Register
			if loc == "" {
				loc = "（位置未知）"
			}
			b.WriteString("- `" + strings.Join(e.Paths, "`、`") + "` → " + e.Handler + "（" + loc + "）\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// renderHTTPRoutesHTML HTTP 路由入口节（html）。
func renderHTTPRoutesHTML(rc *wikiRenderCtx, entries []httpProcEntry, max int) string {
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<h2>HTTP 路由入口</h2><p class="muted">数据源：http_route 节点（构建期识别）→ handler 调用链展开（同 handler 多路由去重）。</p>`)
	expanded, folded := procFold(max, entries)
	curResolver := ""
	for _, e := range expanded {
		if e.Resolver != curResolver {
			curResolver = e.Resolver
			b.WriteString("<h3>[" + htmlEsc(curResolver) + "]</h3>")
		}
		b.WriteString("<h4><code>" + htmlEsc(strings.Join(e.Paths, "</code>、<code>")) + "</code></h4>")
		if e.Register != "" {
			b.WriteString(`<p class="muted">注册点：` + htmlEsc(e.Register) + `</p>`)
		}
		b.WriteString(renderProcChainHTML(rc, e.Chain, httpMissNote(e)))
	}
	if len(folded) > 0 {
		b.WriteString("<details><summary>其余 " + itoa(len(folded)) + " 个入口仅列清单（--max-entries 可调上限）</summary><ul>")
		for _, e := range folded {
			loc := e.Register
			if loc == "" {
				loc = "（位置未知）"
			}
			b.WriteString("<li><code>" + htmlEsc(strings.Join(e.Paths, "</code>、<code>")) + "</code> → " +
				htmlEsc(e.Handler) + "（" + htmlEsc(loc) + "）</li>")
		}
		b.WriteString("</ul></details>")
	}
	return b.String()
}

// grpcMethodMissNote 方法无调用链的说明。
func grpcMethodMissNote(p action.GrpcMethodProc) string {
	if p.Name == "" {
		return "方法名缺失"
	}
	return "索引中无调用链——方法未索引或实现类型缺失（可能未重建索引）"
}
